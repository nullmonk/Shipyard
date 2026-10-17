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
	"shipyard/pkg/builder/drivers"
	"shipyard/pkg/shipfile"
)

// BuildPackageOpts configures a package build targeting a Linux distribution.
type BuildPackageOpts struct {
	// PackageName overrides the package name to build (defaults to Shipfile.PackageName or Name).
	PackageName string

	// Version specifies the target version or tag.
	Version string

	// ArtifactPattern overrides the pattern matching built packages.
	ArtifactPattern string

	// OutputDir is the destination directory for exported artifacts (defaults to build-output/<image>).
	OutputDir string

	// Interactive drops into an interactive shell upon build completion or failure.
	Interactive bool

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
// can diff changes made by `shipyard patch` before applying them via distro tools.
func SnapshotSource(ctr *dagger.Container, sourceDir string) *dagger.Container {
	gitCmd := []string{"git", "-c", "user.name=shipyard", "-c", "user.email=shipyard@localhost"}

	return ctr.
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "*"}).
		WithWorkdir(sourceDir).
		WithExec([]string{"git", "init", "-q"}).
		WithExec([]string{"git", "add", "-A", "-f"}).
		WithExec(append(gitCmd, "commit", "-q", "-m", "pristine"))
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
//  6. Extracts the matching package artifacts to the host.
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

	fmt.Fprintf(out, "[*] Pulling container image: %s\n", image)
	if opts.Verbose {
		fmt.Fprintf(out, "    Package: %s  Driver: %s\n", pkgName, driver.DistroName())
	}

	// Step 1: Distro setup and build deps
	ctr := client.Container().From(image)
	ctr = driver.Setup(ctr)
	fmt.Fprintf(out, "[*] Installing build dependencies for %s...\n", pkgName)
	ctr = driver.InstallBuildDeps(ctr, pkgName)

	sourceDir, err := driver.GetSourceDir(ctx, ctr, pkgName)
	if err != nil {
		return nil, fmt.Errorf("get source directory: %w", err)
	}
	fmt.Fprintf(out, "    Source: %s\n", sourceDir)

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

	// Step 4: Inject self binary and shipfile directory
	self, err := findContainerBinary(sf.Dir)
	if err != nil {
		return nil, err
	}
	selfFile := client.Host().File(self)
	shipfileDir := client.Host().Directory(sf.Dir, dagger.HostDirectoryOpts{
		Exclude: []string{".git/", "__pycache__/", "build-output/", "sources/"},
	})

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
		WithEnvVariable("SHIPYARD_TOPDIR", driver.TopDir())

	if opts.Version != "" {
		ctr = ctr.WithEnvVariable("SHIPYARD_VERSION", opts.Version)
	}

	for k, v := range driver.BuildEnvs() {
		ctr = ctr.WithEnvVariable(k, v)
	}

	// Step 6: patch → apply → build (each step is forced-sync so errors surface immediately)
	ctrPreBuild := ctr

	runStep := func(label string, args []string) (*dagger.Container, error) {
		fmt.Fprintln(out, label)
		fmt.Fprintf(out, "    $ %s\n", strings.Join(args, " "))
		next := ctr.WithExec(args)
		synced, err := next.Sync(ctx)
		if err != nil {
			var execErr *dagger.ExecError
			if errors.As(err, &execErr) {
				if execErr.Stdout != "" {
					// Write to cmdOut so services capture it, and always echo to out so
					// the operator sees it even when cmdOut is discarded.
					fmt.Fprint(cmdOut, execErr.Stdout)
					if cmdOut == io.Discard {
						fmt.Fprint(out, execErr.Stdout)
					}
				}
				if execErr.Stderr != "" {
					fmt.Fprint(cmdErr, execErr.Stderr)
				}
			}
			return nil, err
		}
		// On success, write captured output to the configured writers.
		if stdout, err2 := synced.Stdout(ctx); err2 == nil && stdout != "" {
			fmt.Fprint(cmdOut, stdout)
		}
		return synced, nil
	}

	if synced, err := runStep("[*] Patching source code...", []string{"shipyard", "patch"}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, os.Stderr, ctrPreBuild)
		}
		return nil, fmt.Errorf("patch failed: %w", err)
	} else {
		ctr = synced
	}

	if synced, err := runStep("[*] Applying patches...", []string{"shipyard", "apply"}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, os.Stderr, ctr)
		}
		return nil, fmt.Errorf("apply failed: %w", err)
	} else {
		ctr = synced
	}

	if synced, err := runStep(fmt.Sprintf("[*] Building package: %s...", pkgName), []string{"shipyard", "build"}); err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, os.Stderr, ctr)
		}
		return nil, fmt.Errorf("build failed: %w", err)
	} else {
		ctr = synced
	}

	// Step 7: Extract artifacts
	artifactPattern := opts.ArtifactPattern
	if artifactPattern == "" && sf != nil {
		artifactPattern = sf.Artifacts
	}
	if artifactPattern == "" {
		artifactPattern = driver.GetArtifactPattern(pkgName)
	}

	srcDir := driver.GetArtifactDir()
	outputDir := opts.OutputDir
	if outputDir == "" {
		cleanImg := strings.ReplaceAll(image, ":", "/")
		outputDir = filepath.Join("build-output", cleanImg)
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
			dropIntoShell(ctx, os.Stderr, ctr)
		}
		return nil, fmt.Errorf("artifact collection failed: %w", err)
	}

	ctr, matches, err := ExportArtifacts(ctx, ctr, files, srcDir, artifactPattern, outputDir)
	if err != nil {
		if opts.Interactive {
			dropIntoShell(ctx, os.Stderr, ctr)
		}
		return nil, err
	}

	if opts.Interactive {
		fmt.Fprintln(os.Stderr, "[*] Build successful. Dropping into interactive shell...")
		dropIntoShell(ctx, os.Stderr, ctr)
	}

	return &BuildResult{
		Artifacts: matches,
		OutputDir: outputDir,
		Container: ctr,
	}, nil
}
