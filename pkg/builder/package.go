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

	// RawPatch is an optional unified diff string applied on top of the shipfile changes.
	RawPatch string

	// ArtifactPattern overrides the pattern matching built packages.
	ArtifactPattern string

	// OutputDir is the destination directory for exported artifacts (defaults to build-output/<image>).
	OutputDir string

	// Interactive drops into an interactive shell upon build completion or failure.
	Interactive bool

	// ShipyardPkgDir points to the root directory containing the shipyard Python package.
	ShipyardPkgDir string
}

// SnapshotSource commits the pristine unpacked source so that shipfile changes
// can be diffed out cleanly later.
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
//  2. Unpacks the pristine distro source and snapshots it in git.
//  3. Runs the Python shipfile (@CodePatch) against the source tree in a Python container layer.
//  4. Diff-extracts the changes as a patch, resets the source, and applies it the distro's way.
//  5. Builds the package inside the distro container.
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

	// Step 1: Distro container setup, build deps & pristine source preparation
	ctr := client.Container().From(image)
	ctr = driver.Setup(ctr)
	ctr = driver.InstallBuildDeps(ctr, pkgName)
	ctr = driver.PrepareSource(ctr, pkgName)

	sourceDir, err := driver.GetSourceDir(ctx, ctr, pkgName)
	if err != nil {
		return nil, fmt.Errorf("get source directory: %w", err)
	}
	sourceName := filepath.Base(sourceDir)

	// Step 2: Snapshot pristine source
	ctr = SnapshotSource(ctr, sourceDir)

	// Step 3: Run Python interpreter on pristine source to PATCH it (not build)
	patchContent := ""
	if sf != nil && sf.Dir != "" {
		fmt.Printf("[*] Running Python shipfile from %s on %s...\n", sf.Dir, sourceName)
		patchedSource, err := RunShipfile(ctx, client, ctr.Directory(sourceDir), RunShipfileOpts{
			ShipfileDir:    sf.Dir,
			ShipyardPkgDir: opts.ShipyardPkgDir,
			SourceName:     sourceName,
			Version:        opts.Version,
		})
		if err != nil {
			return nil, fmt.Errorf("run shipfile patcher: %w", err)
		}

		// Put patched source back into container to generate diff
		ctr = ctr.WithDirectory(sourceDir, patchedSource)
		patchContent, err = GeneratePatch(ctx, ctr, sourceDir)
		if err != nil {
			return nil, fmt.Errorf("generate patch from diff: %w", err)
		}
	}

	if opts.RawPatch != "" {
		patchContent += "\n" + opts.RawPatch
	}

	if strings.TrimSpace(patchContent) == "" {
		return nil, fmt.Errorf("the shipfile did not change the source; there is nothing to build")
	}

	lineCount := len(strings.Split(strings.TrimSpace(patchContent), "\n"))
	fmt.Printf("[*] Generated patch (%d lines)\n", lineCount)

	// Step 4: Reset tree to pristine, apply patch the distro's way
	ctr = ctr.WithWorkdir(sourceDir).
		WithExec([]string{"git", "reset", "-q", "--hard"}).
		WithExec([]string{"git", "clean", "-q", "-fdx"}).
		WithExec([]string{"rm", "-rf", ".git"})

	ctr, err = driver.ApplyPatch(ctx, ctr, patchContent, pkgName)
	if err != nil {
		return nil, fmt.Errorf("apply patch: %w", err)
	}

	// Step 5: Perform the build back in the target distro container
	ctrPreBuild := ctr
	ctr = driver.Build(ctr, pkgName)

	// Step 6: Extract artifacts using Shipfile artifacts pattern (or driver default)
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
		// e.g. build-output/debian/bookworm or build-output
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

