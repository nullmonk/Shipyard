package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dagger.io/dagger"
	"shipyard/pkg/builder/drivers"
	"shipyard/pkg/shipfile"
)

// BuildPackageOpts configures a package build targeting a Linux distribution.
type BuildPackageOpts struct {
	// PackageName overrides the package name to build (defaults to Shipfile.PackageName or Name).
	PackageName string

	// Version specifies the target version or tag.
	Version string

	// Variables overrides default variable values defined in the Shipfile.
	// They apply to the host-side steps (setup hash, save); in-container
	// patch/apply/build steps run via the injected shipyard binary.
	Variables map[string]string

	// OutputDir is the destination directory for exported artifacts (defaults to build-output/<image>).
	OutputDir string

	// Copy specifies a local directory to copy into the package source instead of upstream/distro.
	Copy string

	// Interactive drops into an interactive shell upon build completion or failure.
	Interactive bool

	// NoTest skips running @test functions after the build.
	NoTest bool

	// Verbose enables detailed step-by-step progress output.
	Verbose bool

	// Output receives step labels and progress messages (default: os.Stdout).
	Output io.Writer

	// Stdout receives command stdout from each build step (default: io.Discard).
	// Set this to capture or display command output in build services.
	Stdout io.Writer

	// Stderr receives command stderr from each build step (default: os.Stderr).
	Stderr io.Writer
}

// SnapshotSource commits the pristine unpacked source so the in-container build
// can diff changes made by `shipyard patch` before applying them via distro tools,
// or restore changes via `shipyard reset`.
// If git is not installed, SnapshotSource is a no-op.
func SnapshotSource(ctr *dagger.Container, sourceDir string) *dagger.Container {
	gitCmd := "git -c user.name=shipyard -c user.email=shipyard@localhost"
	script := fmt.Sprintf(
		`if command -v git >/dev/null 2>&1; then `+
			`git config --global --add safe.directory "*" && `+
			`git init -q && `+
			`git add -A -f && `+
			`%s commit -q -m pristine; `+
			`fi`,
		gitCmd,
	)

	return ctr.
		WithWorkdir(sourceDir).
		WithExec([]string{"/bin/sh", "-c", script})
}

// GeneratePatch diffs the patched working tree against the pristine initial commit.
func GeneratePatch(ctx context.Context, ctr *dagger.Container, sourceDir string) (string, error) {
	patchCtr := ctr.WithWorkdir(sourceDir).WithExec(
		[]string{"/bin/sh", "-c", "git add -A -f && git diff --cached --binary > /tmp/shipyard.patch"},
	)
	return patchCtr.File("/tmp/shipyard.patch").Contents(ctx)
}

// BuildPackage executes a distro package build:
//  1. Sets up the distro container and installs package build dependencies.
//  2. Snapshots the pristine source in git so the in-container build can diff later.
//  3. Injects the shipyard binary and shipfile into the container.
//  4. Calls `shipyard patch` to apply CodePatch functions.
//  5. Calls `shipyard build` which generates the patch, applies it via distro tools, and builds.
//  6. Installs the built packages (when tests are defined) so tests exercise them.
//  7. Extracts the matching package artifacts to the host.
func BuildPackage(ctx context.Context, client *dagger.Client, sf *shipfile.Shipfile, image string, opts BuildPackageOpts) (*BuildResult, error) {
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
	driver, err := drivers.GetDriver(image)
	if err != nil {
		return nil, err
	}

	pkgName := opts.PackageName
	if pkgName == "" && sf != nil {
		pkgName = sf.PackageName
		if pkgName == "" {
			pkgName = sf.Name
		}
	}
	if pkgName == "" {
		return nil, fmt.Errorf("package name must be specified or defined in Shipfile (PackageName / Name)")
	}

	// Probe image: time a bare sync to detect cache hit vs network pull.
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
	if opts.Verbose {
		fmt.Fprintf(out, "    Package: %s  Driver: %s\n", pkgName, driver.DistroName())
	}

	syncContainer := func(c *dagger.Container, label string) (*dagger.Container, error) {
		fmt.Fprintf(out, "%s... ", label)
		t0 := time.Now()
		synced, err := c.Sync(ctx)
		elapsed := time.Since(t0)
		if err != nil {
			fmt.Fprintln(out, "FAILED")
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
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		fmt.Fprintf(out, "done (%.1fs)\n", elapsed.Seconds())
		return synced, nil
	}

	// Resolve the shipyard binary and shipfile references up front so they can
	// be used both for the early setup step and the later patch step.
	self, err := findContainerBinary(sf.Dir)
	if err != nil {
		return nil, err
	}
	selfFile := client.Host().File(self)
	shipfileDirExcludes := append([]string{".git/", "__pycache__/", "build-output/", "sources/"}, readIgnorePatterns(sf.Dir)...)
	shipfileDir := client.Host().Directory(sf.Dir, dagger.HostDirectoryOpts{
		Exclude: shipfileDirExcludes,
	})

	// Step 1: Distro setup and build deps
	ctr := client.Container().From(image)
	ctr = driver.Setup(ctr)
	if synced, err := syncContainer(ctr, "[*] Setting up distro"); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, os.Stderr, ctr, sf.Dir)
		}
		return nil, err
	} else {
		ctr = synced
	}

	// Run Shipfile setup() before source is fetched so the layer can be cached
	// independently of the package source and patch files.
	// Cache key = image + distro setup + binary + setup commands hash.
	if sf != nil && sf.HasSetup {
		setupHash, err := sf.SetupHash(shipfile.RunContext{Version: opts.Version, Variables: opts.Variables})
		if err != nil {
			return nil, fmt.Errorf("compute setup hash: %w", err)
		}
		hashName := sf.Name
		if opts.Version != "" {
			hashName += "-" + opts.Version
		}
		hashFile := filepath.Join(os.TempDir(), "shipyard-setup-"+hashName+".hash")
		if err := os.WriteFile(hashFile, []byte(setupHash), 0600); err != nil {
			return nil, fmt.Errorf("write setup hash: %w", err)
		}
		defer os.Remove(hashFile)
		setupCtr := ctr.
			WithFile("/usr/local/bin/shipyard", selfFile, dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithFile("/work/shipyard/shipfile.py", client.Host().File(filepath.Join(sf.Dir, "shipfile.py"))).
			WithFile("/work/shipyard/.setup-hash", client.Host().File(hashFile)).
			WithEnvVariable("SHIPYARD_IN_CONTAINER", "1").
			WithEnvVariable("SHIPFILE", "/work/shipyard/shipfile.py")
		if opts.Version != "" {
			setupCtr = setupCtr.WithEnvVariable("SHIPYARD_VERSION", opts.Version)
		}
		if synced, err := syncContainer(setupCtr.WithExec([]string{"shipyard", "setup"}), "[*] Setting up"); err != nil {
			if opts.Interactive {
				dropIntoShell(ctx, client, os.Stderr, setupCtr, sf.Dir)
			}
			return nil, err
		} else {
			ctr = synced
		}
	}

	ctr = driver.InstallBuildDeps(ctr, pkgName)
	if synced, err := syncContainer(ctr, fmt.Sprintf("[*] Installing build deps for %s", pkgName)); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, os.Stderr, ctr, sf.Dir)
		}
		return nil, err
	} else {
		ctr = synced
	}

	sourceDir, err := driver.GetSourceDir(ctx, ctr, pkgName)
	if err != nil {
		return nil, fmt.Errorf("get source directory: %w", err)
	}
	fmt.Fprintf(out, "    Source: %s\n", sourceDir)

	if opts.Copy != "" {
		copyDir, err := PullSource(ctx, client, SourceConfig{LocalDir: opts.Copy})
		if err != nil {
			return nil, fmt.Errorf("pull copy source: %w", err)
		}
		ctr = ctr.WithDirectory(sourceDir, copyDir)
		fmt.Fprintf(out, "[*] Overriding package source with copy from %s\n", opts.Copy)
	}

	// Step 2: Copy pristine source for later diff generation and interactive reset.
	ctr = ctr.
		WithExec([]string{"/bin/sh", "-c", fmt.Sprintf("cp -a '%s' /tmp/.shipyard-pristine", sourceDir)}).
		WithEnvVariable("SHIPYARD_PRISTINE", "/tmp/.shipyard-pristine")

	// Step 3: Read distro version from /etc/os-release
	distroVersion, err := ctr.
		WithExec([]string{"/bin/sh", "-c", `grep '^VERSION_ID=' /etc/os-release | cut -d= -f2 | tr -d '"'`}).
		Stdout(ctx)
	if err != nil {
		distroVersion = ""
	}
	distroVersion = strings.TrimSpace(distroVersion)
	if distroVersion != "" {
		fmt.Fprintf(out, "    Distro: %s %s\n", driver.DistroName(), distroVersion)
	}

	// Step 4: Inject self binary and full shipfile directory (patches, resources)
	if opts.Verbose {
		fmt.Fprintln(out, "[*] Injecting shipyard binary and shipfile directory...")
	}
	ctr = ctr.
		WithFile("/usr/local/bin/shipyard", selfFile, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithDirectory("/work/shipyard", shipfileDir)

	// Step 5: Set environment variables for in-container execution
	ctr = ctr.
		WithEnvVariable("SHIPYARD_IN_CONTAINER", "1").
		WithEnvVariable("SHIPYARD_MODE", "package").
		WithEnvVariable("SHIPFILE", "/work/shipyard/shipfile.py").
		WithEnvVariable("SHIPYARD_SOURCE", sourceDir).
		WithEnvVariable("SHIPYARD_PACKAGE", pkgName).
		WithEnvVariable("SHIPYARD_DISTRO", driver.DistroName()).
		WithEnvVariable("SHIPYARD_DISTRO_VERSION", distroVersion).
		WithEnvVariable("SHIPYARD_TOPDIR", driver.TopDir()).
		WithWorkdir(sourceDir)

	if opts.Version != "" {
		ctr = ctr.WithEnvVariable("SHIPYARD_VERSION", opts.Version)
	}

	for k, v := range driver.BuildEnvs() {
		ctr = ctr.WithEnvVariable(k, v)
	}

	// Step 6: patch → apply → build (each step is forced-sync so errors surface immediately)
	ctrPreBuild := ctr

	runStep := func(label string, args []string) (*dagger.Container, error) {
		fmt.Fprintf(out, "%s... ", label)
		if opts.Verbose {
			fmt.Fprintf(out, "\n    $ %s\n", strings.Join(args, " "))
		}
		t0 := time.Now()
		next := ctr.WithExec(args)
		synced, err := next.Sync(ctx)
		elapsed := time.Since(t0)
		if err != nil {
			fmt.Fprintln(out, "FAILED")
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
		fmt.Fprintf(out, "done (%.1fs)\n", elapsed.Seconds())
		if stdout, err2 := synced.Stdout(ctx); err2 == nil && stdout != "" {
			fmt.Fprint(cmdOut, stdout)
		}
		return synced, nil
	}

	if synced, err := runStep("[*] Patching source code", []string{"shipyard", "patch"}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, os.Stderr, ctrPreBuild, sf.Dir)
		}
		return nil, fmt.Errorf("patch failed: %w", err)
	} else {
		ctr = synced
	}

	if synced, err := runStep("[*] Applying patches", []string{"shipyard", "apply"}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, os.Stderr, ctr, sf.Dir)
		}
		return nil, fmt.Errorf("apply failed: %w", err)
	} else {
		ctr = synced
	}

	if synced, err := runStep(fmt.Sprintf("[*] Building package: %s", pkgName), []string{"shipyard", "build"}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, client, os.Stderr, ctr, sf.Dir)
		}
		return nil, fmt.Errorf("build failed: %w", err)
	} else {
		ctr = synced
	}

	if sf != nil && sf.IsTest() && !opts.NoTest {
		// Install the freshly built packages with the distro package manager
		// so @test functions run against the new packages (which tests may
		// assume are installed), not the distro-shipped versions.
		if synced, err := runStep("[*] Installing built packages", []string{"/bin/sh", "-c", driver.InstallArtifactsScript()}); err != nil {
			if opts.Interactive {
				dropIntoShell(ctx, client, os.Stderr, ctr, sf.Dir)
			}
			return nil, fmt.Errorf("install built packages for tests: %w", err)
		} else {
			ctr = synced
		}
		if synced, err := runStep("[*] Running tests", []string{"shipyard", "test"}); err != nil {
			if opts.Interactive {
				dropIntoShell(ctx, client, os.Stderr, ctr, sf.Dir)
			}
			return nil, fmt.Errorf("tests failed: %w", err)
		} else {
			ctr = synced
		}
	}

	// Step 7: Extract artifacts
	srcDir := driver.GetArtifactDir()
	outputDir := opts.OutputDir
	if outputDir == "" {
		cleanImg := strings.ReplaceAll(image, ":", "/")
		outputDir = filepath.Join("build-output", cleanImg)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory %s: %w", outputDir, err)
	}

	// If the shipfile defines save(), run it with a DaggerBuilder pointed at the
	// artifact directory. save() calls Save(src, dst) to declare what to export.
	if sf != nil && sf.HasSave {
		db := &DaggerBuilder{
			ctx:       ctx,
			client:    client,
			sfDir:     sf.Dir,
			out:       out,
			cmdOut:    cmdOut,
			cmdErr:    cmdErr,
			verbose:   opts.Verbose,
			Container: ctr,
		}
		if err := db.Workdir(srcDir); err != nil {
			return nil, fmt.Errorf("workdir to artifact dir: %w", err)
		}
		if err := sf.RunSaveWithBuilder(db, shipfile.RunContext{Version: opts.Version, Variables: opts.Variables}); err != nil {
			if opts.Interactive {
				dropIntoShell(ctx, client, os.Stderr, ctr, sf.Dir)
			}
			return nil, err
		}
		fmt.Fprintln(out, "[*] Saving artifacts...")
		matches, err := exportArtifacts(ctx, db.Saves, outputDir, opts.Verbose, out)
		if err != nil {
			return nil, err
		}
		return &BuildResult{
			Artifacts: matches,
			OutputDir: outputDir,
			Container: ctr,
		}, nil
	}

	return nil, fmt.Errorf("shipfile has no save() method; add save() to declare what to export")
}
