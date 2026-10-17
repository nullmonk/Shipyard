package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"shipyard/pkg/shipfile"
)

// BuildResult contains the outcome of a successful build.
type BuildResult struct {
	Artifacts []string
	OutputDir string
	Container *dagger.Container
}

// BuildShipfileOpts defines the options for a full shipfile build pipeline.
type BuildShipfileOpts struct {
	// Image overrides the Shipfile's BaseImage.
	Image string

	// Version specifies the target version or tag to build.
	Version string

	// ArtifactPattern overrides the Shipfile's Artifacts regex.
	ArtifactPattern string

	// OutputDir overrides the destination directory for exported artifacts.
	OutputDir string

	// Copy specifies a local directory to copy into the container as source instead of git.
	Copy string

	// Interactive opens an interactive shell in the container.
	Interactive bool

	// NoTest skips running @test functions after the build.
	NoTest bool

	// Verbose enables detailed step-by-step progress output.
	Verbose bool

	// Output receives step labels and progress messages (default: os.Stdout).
	Output io.Writer

	// Stdout receives command stdout from the build step (default: io.Discard).
	// Set this to capture or display command output in build services.
	Stdout io.Writer

	// Stderr receives command stderr from the build step (default: os.Stderr).
	Stderr io.Writer
}

// BuildShipfile coordinates a generic (non-distro-package) build:
//  1. Pull the pristine source code (from Git or local directory).
//  2. Inject the shipyard binary and shipfile into the container.
//  3. Call `shipyard build` which runs setup() and build() from the Shipfile.
//  4. Export matching artifacts.
func BuildShipfile(ctx context.Context, client *dagger.Client, sf *shipfile.Shipfile, opts BuildShipfileOpts) (*BuildResult, error) {
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}
	cmdOut := opts.Stdout
	if cmdOut == nil {
		cmdOut = io.Discard
	}
	cmdErr := opts.Stderr
	if cmdErr == nil {
		cmdErr = os.Stderr
	}
	image := opts.Image
	if image == "" {
		image = sf.BaseImage
	}
	if image == "" {
		return nil, fmt.Errorf("no base image specified; provide --image or set BaseImage in Shipfile")
	}

	ver := opts.Version
	if ver == "" {
		ver = "src"
	}
	sourceName := fmt.Sprintf("%s-%s", sf.Name, ver)

	workdir := "/work/" + sourceName

	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join("build-output", "standalone", sf.Name)
	}

	// Pull pristine source code
	var srcCfg SourceConfig

	if opts.Copy != "" {
		srcCfg.LocalDir = opts.Copy
		fmt.Fprintf(out, "[*] Prepping source: %s (copy: %s)\n", sf.Name, opts.Copy)
	} else {
		localSource := filepath.Join(sf.Dir, "sources", sf.Name)
		if fi, err := os.Stat(localSource); err == nil && fi.IsDir() {
			srcCfg.LocalDir = localSource
			fmt.Fprintf(out, "[*] Prepping source: %s (local: %s)\n", sf.Name, localSource)
		} else if sf.Url != "" && opts.Version != "" {
			srcCfg.Url = sf.Url
			srcCfg.Tag = opts.Version
			fmt.Fprintf(out, "[*] Prepping source: %s @ %s\n", sf.Name, opts.Version)
			if opts.Verbose {
				fmt.Fprintf(out, "    Cloning %s\n", sf.Url)
			}
		} else if sf.Url != "" && sf.Branch != "" {
			srcCfg.Url = sf.Url
			srcCfg.Branch = sf.Branch
			fmt.Fprintf(out, "[*] Prepping source: %s (branch: %s)\n", sf.Name, sf.Branch)
			if opts.Verbose {
				fmt.Fprintf(out, "    Cloning %s\n", sf.Url)
			}
		} else {
			srcCfg.LocalDir = sf.Dir
			fmt.Fprintf(out, "[*] Prepping source: %s (working tree)\n", sf.Name)
		}
	}

	pristineSource, err := PullSource(ctx, client, srcCfg)
	if err != nil {
		return nil, fmt.Errorf("pull source: %w", err)
	}

	// Inject self binary and shipfile directory.
	// Prefer a pre-built static binary (required for musl-based images like Alpine).
	// go-run binaries are dynamically linked against glibc and will not run in Alpine.
	self, err := findContainerBinary(sf.Dir)
	if err != nil {
		return nil, err
	}
	selfFile := client.Host().File(self)
	shipfileDir := client.Host().Directory(sf.Dir, dagger.HostDirectoryOpts{
		Exclude: []string{".git/", "__pycache__/", "build-output/", "sources/"},
	})

	fmt.Fprintf(out, "[*] Pulling container image: %s\n", image)
	if opts.Verbose {
		fmt.Fprintf(out, "    Injecting shipyard binary and shipfile directory\n")
	}
	ctr := client.Container().
		From(image).
		WithDirectory(workdir, pristineSource).
		WithFile("/usr/local/bin/shipyard", selfFile, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithDirectory("/work/shipyard", shipfileDir).
		WithEnvVariable("SHIPYARD_IN_CONTAINER", "1").
		WithEnvVariable("SHIPYARD_MODE", "build").
		WithEnvVariable("SHIPFILE", "/work/shipyard/shipfile.py").
		WithEnvVariable("SHIPYARD_SOURCE", workdir).
		WithEnvVariable("SHIPYARD_VERSION", ver).
		WithWorkdir(workdir)

	// Snapshot source so 'shipyard reset' can restore it in an interactive session.
	if opts.Interactive {
		ctr = SnapshotSource(ctr, workdir)
	}

	runStep := func(label string, args []string) (*dagger.Container, error) {
		fmt.Fprintln(out, label)
		fmt.Fprintf(out, "    $ %s\n", strings.Join(args, " "))
		next := ctr.WithExec(args)
		synced, err := next.Sync(ctx)
		if err != nil {
			var execErr *dagger.ExecError
			if errors.As(err, &execErr) {
				if execErr.Stdout != "" {
					outStr := execErr.Stdout
					if !strings.HasSuffix(outStr, "\n") {
						outStr += "\n"
					}
					fmt.Fprint(cmdOut, outStr)
					if cmdOut == io.Discard {
						fmt.Fprint(out, outStr)
					}
				}
				if execErr.Stderr != "" {
					errStr := execErr.Stderr
					if !strings.HasSuffix(errStr, "\n") {
						errStr += "\n"
					}
					fmt.Fprint(cmdErr, errStr)
				}
			}
			return nil, err
		}
		if stdout, err2 := synced.Stdout(ctx); err2 == nil && stdout != "" {
			fmt.Fprint(cmdOut, stdout)
		}
		return synced, nil
	}

	ctrPreBuild := ctr
	if synced, err := runStep(fmt.Sprintf("[*] Building %s...", sf.Name), []string{"shipyard", "build"}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, os.Stderr, ctrPreBuild)
		}
		return nil, fmt.Errorf("build failed: %w", err)
	} else {
		ctr = synced
	}

	if sf.IsTest() && !opts.NoTest {
		if synced, err := runStep("[*] Running tests...", []string{"shipyard", "test"}); err != nil {
			if opts.Interactive {
				dropIntoShell(ctx, client, os.Stderr, ctr)
			}
			return nil, fmt.Errorf("tests failed: %w", err)
		} else {
			ctr = synced
		}
	}

	artifactPattern := opts.ArtifactPattern
	if artifactPattern == "" {
		artifactPattern = sf.Artifacts
	}
	if artifactPattern == "" {
		artifactPattern = ".*"
	}

	srcDir := sf.ArtifactDir
	if srcDir == "" {
		srcDir = workdir
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory %s: %w", outputDir, err)
	}

	if opts.Verbose {
		fmt.Fprintf(out, "[*] Collecting artifacts from %s matching '%s'...\n", srcDir, artifactPattern)
	} else {
		fmt.Fprintln(out, "[*] Collecting artifacts...")
	}
	files, err := FindFiles(ctx, ctr, srcDir)
	if err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, os.Stderr, ctr)
		}
		return nil, fmt.Errorf("artifact collection failed: %w", err)
	}

	ctr, matches, err := ExportArtifacts(ctx, ctr, files, srcDir, artifactPattern, outputDir, out)
	if err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, os.Stderr, ctr)
		}
		return nil, err
	}

	return &BuildResult{
		Artifacts: matches,
		OutputDir: outputDir,
		Container: ctr,
	}, nil
}

// findContainerBinary returns a path to a shipyard binary suitable for injection
// into containers. Statically-linked builds are required for musl-based images
// (e.g. Alpine); `go run` produces a glibc-linked binary that fails in those
// environments. Checked in order: bin/shipyard-go, bin/shipyard (in sfDir then
// CWD), then the running executable as a last resort.
func findContainerBinary(sfDir string) (string, error) {
	names := []string{"bin/shipyard", "bin/shipyard-go"}
	dirs := []string{sfDir, ".", "../shipyard"}
	for _, name := range names {
		for _, dir := range dirs {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return self, nil
}
