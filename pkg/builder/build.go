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

	// Variables overrides default variable values defined in the Shipfile.
	// Unknown keys must be filtered by the caller: Shipfile.NewContext
	// returns an error for undeclared variables.
	Variables map[string]string

	// OutputDir overrides the destination directory for exported artifacts.
	OutputDir string

	// Copy specifies a local directory to copy into the container as source instead of git.
	Copy string

	// Interactive opens an interactive shell in the container.
	Interactive bool

	// NoTest skips running @test functions after the build.
	NoTest bool

	// TestSystems, when non-empty, enables multi-system test fan-out: no
	// in-place test run happens in the build container. Instead one fresh
	// test container is started per image ref (FROM the ref, then staged
	// with the build outputs), each emitting its own
	// [drydock:phase:test:<ref>] section. Empty preserves the legacy
	// behavior: in-place tests labeled 'build'.
	TestSystems []string

	// MirrorBaseURL is accepted and stored for forward compatibility but
	// NOT used in v1: fixtures resolve from distro repos only (no mirror
	// configs are plumbed through). Documented as reserved-for-future in
	// the --mirror-base-url flag help.
	MirrorBaseURL string

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

	mOut := PhaseOut(opts.Stdout)
	if opts.MirrorBaseURL != "" {
		fmt.Fprintf(out, "[*] Note: --mirror-base-url %q is reserved for future use; v1 resolves fixtures from distro repos only (mirror configs are not plumbed through).\n", opts.MirrorBaseURL)
	}

	// --- setup phase: shipfile setup() only, split out so its output is
	// captured under [drydock:phase:setup].
	EmitMarker(mOut, PhaseSetup)
	if err := sf.RunSetupWithBuilder(db, shipfile.RunContext{Version: ver, Variables: opts.Variables}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, out, db.Container, sf.Dir)
		}
		return nil, err
	}

	// --- build phase ---
	EmitMarker(mOut, PhaseBuild)
	fmt.Fprintf(out, "[*] Building %s\n", sf.Name)
	if err := sf.RunBuildWithBuilder(db, shipfile.RunContext{Version: ver, Variables: opts.Variables}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, out, db.Container, sf.Dir)
		}
		return nil, err
	}
	// Record save() outputs here — the same point in the lifecycle as the
	// legacy combined run — so Save snapshots predate any test execution.
	if err := sf.RunSaveWithBuilder(db, shipfile.RunContext{Version: ver, Variables: opts.Variables}); err != nil {
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

	runTests := sf != nil && sf.IsTest() && !opts.NoTest
	if !runTests && len(opts.TestSystems) > 0 {
		fmt.Fprintln(out, "    (no @test functions to run; ignoring --test-system)")
	}
	if runTests && len(opts.TestSystems) == 0 {
		EmitMarker(mOut, TestPhaseMarker(LegacyTestLabel))
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

	if runTests && len(opts.TestSystems) > 0 {
		// --- test phase fan-out: one fresh container per --test-system ---
		// NO in-place test run happens in this mode; each system gets its
		// own [drydock:phase:test:<ref>] section. Any single test system
		// failure fails the overall run.
		if err := runGenericFanOutTests(ctx, client, sf, ctr, db.Saves, ver, opts.TestSystems, opts.Interactive, opts.Verbose, out, mOut, cmdOut, cmdErr); err != nil {
			return nil, err
		}
	}

	// --- publish phase ---
	EmitMarker(mOut, PhasePublish)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory %s: %w", outputDir, err)
	}

	if len(db.Saves) == 0 {
		return nil, fmt.Errorf("build produced no artifacts: add Save() calls in build() to export outputs")
	}

	fmt.Fprintln(out, "[*] Saving artifacts...")
	matches, err := exportArtifacts(ctx, db.Saves, outputDir, opts.Verbose, out)
	if err != nil {
		return nil, err
	}
	EmitMarker(mOut, PhaseDone)
	return &BuildResult{
		Artifacts: matches,
		OutputDir: outputDir,
		Container: ctr,
	}, nil
}

func exportContainerPath(ctx context.Context, ctr *dagger.Container, srcPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	if _, err := ctr.File(srcPath).Export(ctx, dstPath); err != nil {
		if err2 := os.MkdirAll(dstPath, 0o755); err2 != nil {
			return fmt.Errorf("create dir: %w", err2)
		}
		if _, err2 := ctr.Directory(srcPath).Export(ctx, dstPath); err2 != nil {
			return fmt.Errorf("export %s: %w", srcPath, err2)
		}
	}
	return nil
}

// exportArtifacts exports the saved artifacts to outputDir on the host.
// Note: The artifacts output is designed as a "container" with a raw layer for the files
// and a custom description and title.
func exportArtifacts(ctx context.Context, saves []SaveOp, outputDir string, verbose bool, out io.Writer) ([]string, error) {
	var matches []string
	for _, save := range saves {
		targetDst := save.DstPath
		if targetDst == "." || targetDst == "" {
			targetDst = filepath.Base(save.SrcPath)
		} else if strings.HasSuffix(targetDst, "/") {
			targetDst = filepath.Join(targetDst, filepath.Base(save.SrcPath))
		}
		dstPath := filepath.Join(outputDir, targetDst)
		if verbose {
			if save.IsDir {
				fmt.Fprintf(out, "    %s → %s/\n", save.SrcPath, targetDst)
			} else {
				fmt.Fprintf(out, "    %s → %s\n", save.SrcPath, targetDst)
			}
		}
		if save.IsDir {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return nil, fmt.Errorf("create dir: %w", err)
			}
			if _, err := save.Container.Directory(save.SrcPath).Export(ctx, dstPath); err != nil {
				return nil, fmt.Errorf("export %s: %w", save.DstPath, err)
			}
		} else {
			if err := exportContainerPath(ctx, save.Container, save.SrcPath, dstPath); err != nil {
				return nil, fmt.Errorf("save %s: %w", save.DstPath, err)
			}
		}

		fi, err := os.Stat(dstPath)
		if err == nil && fi.IsDir() {
			_ = filepath.Walk(dstPath, func(p string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					matches = append(matches, p)
				}
				return nil
			})
		} else {
			matches = append(matches, dstPath)
		}
	}
	return matches, nil
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
