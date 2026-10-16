package builder

import (
	"context"
	"fmt"
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

	fmt.Printf("[*] Starting package build for %s on %s\n", pkgName, image)

	// Step 1: Distro setup and build deps
	ctr := client.Container().From(image)
	ctr = driver.Setup(ctr)
	ctr = driver.InstallBuildDeps(ctr, pkgName)

	sourceDir, err := driver.GetSourceDir(ctx, ctr, pkgName)
	if err != nil {
		return nil, fmt.Errorf("get source directory: %w", err)
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

	// Step 4: Inject self binary and shipfile directory
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	selfFile := client.Host().File(self)
	shipfileDir := client.Host().Directory(sf.Dir, dagger.HostDirectoryOpts{
		Exclude: []string{".git/", "__pycache__/", "build-output/", "sources/"},
	})

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

	// Step 6: patch → apply → build
	ctrPreBuild := ctr
	fmt.Println("[*] Running shipyard patch...")
	ctr = ctr.WithExec([]string{"shipyard", "patch"})

	fmt.Println("[*] Running shipyard apply...")
	ctr = ctr.WithExec([]string{"shipyard", "apply"})

	fmt.Println("[*] Running shipyard build...")
	ctr = ctr.WithExec([]string{"shipyard", "build"})

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

	fmt.Printf("[*] Inspecting artifacts in %s matching '%s'...\n", srcDir, artifactPattern)
	files, err := FindFiles(ctx, ctr, srcDir)
	if err != nil {
		if opts.Interactive {
			fmt.Printf("[!] Build failed: %v. Dropping into interactive shell...\n", err)
			target := ctrPreBuild
			if target == nil {
				target = ctr
			}
			_, _ = target.Terminal().Sync(ctx)
		}
		return nil, fmt.Errorf("package build failed: %w", err)
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
