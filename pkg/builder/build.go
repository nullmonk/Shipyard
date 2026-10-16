package builder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

	// Interactive opens an interactive shell in the container.
	Interactive bool
}

// BuildShipfile coordinates a generic (non-distro-package) build:
//  1. Pull the pristine source code (from Git or local directory).
//  2. Inject the shipyard binary and shipfile into the container.
//  3. Call `shipyard build` which runs setup() and build() from the Shipfile.
//  4. Export matching artifacts.
func BuildShipfile(ctx context.Context, client *dagger.Client, sf *shipfile.Shipfile, opts BuildShipfileOpts) (*BuildResult, error) {
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
		outputDir = "build-output"
	}

	// Pull pristine source code
	fmt.Printf("[*] Pulling source code for %s...\n", sf.Name)
	var srcCfg SourceConfig

	localSource := filepath.Join(sf.Dir, "sources", sf.Name)
	if fi, err := os.Stat(localSource); err == nil && fi.IsDir() {
		// Pre-cloned source takes priority over everything.
		srcCfg.LocalDir = localSource
	} else if sf.Url != "" && opts.Version != "" {
		// A specific version was requested — clone that tag/branch from the URL.
		srcCfg.Url = sf.Url
		srcCfg.Tag = opts.Version
	} else if sf.Url != "" && sf.Branch != "" {
		srcCfg.Url = sf.Url
		srcCfg.Branch = sf.Branch
	} else {
		// No pre-cloned source and no version pinned: build from the shipfile directory.
		// This is the typical dev workflow (current working tree, unpushed changes, etc.).
		srcCfg.LocalDir = sf.Dir
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

	fmt.Printf("[*] Setting up build container: %s\n", image)
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

	ctrPreBuild := ctr
	fmt.Println("[*] Running shipyard build...")
	buildCtr := ctr.WithExec([]string{"shipyard", "build"})

	// Force execution and capture any build error with its output.
	if _, err := buildCtr.Sync(ctx); err != nil {
		var execErr *dagger.ExecError
		if errors.As(err, &execErr) {
			if execErr.Stdout != "" {
				fmt.Print(execErr.Stdout)
			}
			if execErr.Stderr != "" {
				fmt.Fprint(os.Stderr, execErr.Stderr)
			}
		}
		if opts.Interactive {
			fmt.Printf("[!] Build failed: %v. Dropping into interactive shell...\n", err)
			_, _ = ctrPreBuild.Terminal().Sync(ctx)
		}
		return nil, fmt.Errorf("build failed: %w", err)
	}
	ctr = buildCtr

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

	fmt.Printf("[*] Inspecting artifacts in %s matching '%s'...\n", srcDir, artifactPattern)
	files, err := FindFiles(ctx, ctr, srcDir)
	if err != nil {
		if opts.Interactive {
			fmt.Printf("[!] Artifact collection failed: %v. Dropping into interactive shell...\n", err)
			_, _ = ctr.Terminal().Sync(ctx)
		}
		return nil, fmt.Errorf("artifact collection failed: %w", err)
	}

	ctr, matches, err := ExportArtifacts(ctx, ctr, files, srcDir, artifactPattern, outputDir)
	if err != nil {
		if opts.Interactive {
			fmt.Printf("[!] Artifact export failed: %v. Dropping into interactive shell...\n", err)
			_, _ = ctr.Terminal().Sync(ctx)
		}
		return nil, err
	}

	if opts.Interactive {
		fmt.Println("[*] Build successful. Dropping into interactive shell...")
		_, _ = ctr.Terminal().Sync(ctx)
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
	dirs := []string{sfDir, "."}
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
