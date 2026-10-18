package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"shipyard/pkg/shipfile"

	"dagger.io/dagger"
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

// BuildShipfile drives a generic (non-distro-package) build. The shipfile's
// build() (and setup(), if defined) run on the HOST via a DaggerBuilder: every
// shipyard.From/Run/Copy/Write/Source/Workdir call accumulates a Dagger layer
// directly without injecting a shipyard binary into the container.
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

	ver := opts.Version
	if ver == "" {
		ver = "src"
	}

	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join("build-output", "standalone", sf.Name)
	}

	// Pull the pristine source so build() can call Source(dest) to mount it.
	var pristineSource *dagger.Directory
	if opts.Copy != "" {
		ps, err := PullSource(ctx, client, SourceConfig{LocalDir: opts.Copy})
		if err != nil {
			return nil, fmt.Errorf("pull source: %w", err)
		}
		pristineSource = ps
		fmt.Fprintf(out, "[*] Source: %s (copy: %s)\n", sf.Name, opts.Copy)
	} else {
		localSource := filepath.Join(sf.Dir, "sources", sf.Name)
		switch {
		case func() bool { fi, err := os.Stat(localSource); return err == nil && fi.IsDir() }():
			ps, err := PullSource(ctx, client, SourceConfig{LocalDir: localSource})
			if err != nil {
				return nil, fmt.Errorf("pull source: %w", err)
			}
			pristineSource = ps
			fmt.Fprintf(out, "[*] Source: %s (local)\n", sf.Name)
		case sf.Url != "" && opts.Version != "":
			ps, err := PullSource(ctx, client, SourceConfig{Url: sf.Url, Tag: opts.Version})
			if err != nil {
				return nil, fmt.Errorf("pull source: %w", err)
			}
			pristineSource = ps
			fmt.Fprintf(out, "[*] Source: %s @ %s\n", sf.Name, opts.Version)
		case sf.Url != "" && sf.Branch != "":
			ps, err := PullSource(ctx, client, SourceConfig{Url: sf.Url, Branch: sf.Branch})
			if err != nil {
				return nil, fmt.Errorf("pull source: %w", err)
			}
			pristineSource = ps
			fmt.Fprintf(out, "[*] Source: %s (branch: %s)\n", sf.Name, sf.Branch)
		case sf.Url != "":
			ps, err := PullSource(ctx, client, SourceConfig{Url: sf.Url})
			if err != nil {
				return nil, fmt.Errorf("pull source: %w", err)
			}
			pristineSource = ps
			fmt.Fprintf(out, "[*] Source: %s (HEAD)\n", sf.Name)
		}
	}

	// Seed container from BaseImage (or opts.Image) if set. build() may call
	// From() to override it. If neither is set, From() in build() is required.
	image := opts.Image
	if image == "" {
		image = sf.BaseImage
	}

	var baseCtr *dagger.Container
	if image != "" {
		fmt.Fprintf(out, "[*] Image: %s... ", image)
		t0 := time.Now()
		if _, err := client.Container().From(image).Sync(ctx); err != nil {
			fmt.Fprintln(out, "FAILED")
			return nil, fmt.Errorf("pull image %s: %w", image, err)
		}
		if elapsed := time.Since(t0); elapsed < 2*time.Second {
			fmt.Fprintln(out, "cached")
		} else {
			fmt.Fprintf(out, "pulled (%.1fs)\n", elapsed.Seconds())
		}
		baseCtr = client.Container().From(image)
	} else {
		baseCtr = client.Container()
	}

	db := &DaggerBuilder{
		ctx:       ctx,
		client:    client,
		sfDir:     sf.Dir,
		source:    pristineSource,
		out:       out,
		cmdOut:    cmdOut,
		cmdErr:    cmdErr,
		verbose:   opts.Verbose,
		Container: baseCtr,
	}

	fmt.Fprintf(out, "[*] Building %s\n", sf.Name)
	if err := sf.RunWithBuilder(db, shipfile.RunContext{Version: ver}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, out, db.Container, sf.Dir)
		}
		return nil, err
	}

	ctr := db.Container
	if ctr == nil {
		return nil, fmt.Errorf("build completed without a container (was From() called?)")
	}

	if opts.Interactive {
		dropIntoShell(ctx, client, out, ctr, sf.Dir)
	}

	if sf != nil && sf.IsTest() && !opts.NoTest {
		shipfileHostFile := client.Host().File(filepath.Join(sf.Dir, "shipfile.py"))

		testCtr := ctr.
			WithFile("/work/shipyard/shipfile.py", shipfileHostFile).
			WithFile("shipfile.py", shipfileHostFile).
			WithEnvVariable("SHIPYARD_IN_CONTAINER", "1").
			WithEnvVariable("SHIPFILE", "/work/shipyard/shipfile.py")

		if sf.Name == "shipyard" {
			// Prefer the container's own freshly-built binary, guaranteed compatible with container OS/libc
			testCtr = testCtr.WithExec([]string{"/bin/sh", "-c", "if [ -f bin/shipyard ]; then cp bin/shipyard /usr/local/bin/shipyard; fi"})
		} else {
			self, err := findContainerBinary(sf.Dir)
			if err != nil {
				return nil, fmt.Errorf("resolve shipyard binary for in-container tests: %w", err)
			}
			selfFile := client.Host().File(self)
			testCtr = testCtr.WithFile("/usr/local/bin/shipyard", selfFile, dagger.ContainerWithFileOpts{Permissions: 0o755})
		}

		if ver != "" {
			testCtr = testCtr.WithEnvVariable("SHIPYARD_VERSION", ver)
		}

		fmt.Fprintln(out, "[*] Running tests in container...")
		testExec := testCtr.WithExec([]string{"shipyard", "test"})
		synced, err := testExec.Sync(ctx)
		if err != nil {
			var execErr *dagger.ExecError
			if errors.As(err, &execErr) {
				if execErr.Stdout != "" {
					outStr := execErr.Stdout
					if !strings.HasSuffix(outStr, "\n") {
						outStr += "\n"
					}
					fmt.Fprint(cmdOut, outStr)
				}
				if execErr.Stderr != "" {
					errStr := execErr.Stderr
					if !strings.HasSuffix(errStr, "\n") {
						errStr += "\n"
					}
					fmt.Fprint(cmdErr, errStr)
				}
			}
			if opts.Interactive {
				dropIntoShell(ctx, client, out, testCtr, sf.Dir)
			}
			return nil, fmt.Errorf("tests failed: %w", err)
		}
		stdout, _ := synced.Stdout(ctx)
		if stdout != "" {
			fmt.Fprint(cmdOut, stdout)
		}
		ctr = synced
		db.Container = ctr
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory %s: %w", outputDir, err)
	}

	if len(db.Saves) == 0 {
		return nil, fmt.Errorf("build produced no artifacts: add Save() calls in build() to export outputs")
	}

	fmt.Fprintln(out, "[*] Saving artifacts...")
	var matches []string
	for _, save := range db.Saves {
		dstPath := filepath.Join(outputDir, save.DstPath)
		if opts.Verbose {
			fmt.Fprintf(out, "    %s → %s\n", save.SrcPath, save.DstPath)
		}
		// Try file export first; fall back to directory export to preserve layout.
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return nil, fmt.Errorf("save %s: create parent dir: %w", save.DstPath, err)
		}
		if _, err := save.Container.File(save.SrcPath).Export(ctx, dstPath); err != nil {
			if err2 := os.MkdirAll(dstPath, 0o755); err2 != nil {
				return nil, fmt.Errorf("save %s: create dir: %w", save.DstPath, err2)
			}
			if _, err2 := save.Container.Directory(save.SrcPath).Export(ctx, dstPath); err2 != nil {
				return nil, fmt.Errorf("save %s: %w", save.SrcPath, err2)
			}
		}
		matches = append(matches, dstPath)
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
			if dir == "" {
				continue
			}
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return filepath.Abs(p)
			}
		}
	}
	if p, err := exec.LookPath("shipyard"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs, nil
			}
		}
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return self, nil
}
