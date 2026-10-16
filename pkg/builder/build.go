package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"shipyard/pkg/shipfile"
)

// BuildOpts configures a container build.
type BuildOpts struct {
	// BaseImage is the container image in which Setup and Build run.
	BaseImage string

	// SourceName is the name of the project/source (e.g. "mytool-1.0").
	SourceName string

	// Source is the directory containing the (optionally patched) source code.
	Source *dagger.Directory

	// Workdir is the container directory where the source is mounted.
	// Defaults to "/work/<SourceName>".
	Workdir string

	// RawPatch is an optional unified diff string applied with `patch -p1`.
	RawPatch string

	// Setup is an optional shell script run before Build to prepare dependencies.
	Setup string

	// Build is the shell command/script that builds the project.
	Build string

	// ArtifactPattern is a regex pattern matching files to export.
	ArtifactPattern string

	// ArtifactDir is the container directory searched for artifacts.
	// Defaults to Workdir.
	ArtifactDir string

	// OutputDir is the destination directory on the host for exported artifacts.
	// Defaults to "build-output".
	OutputDir string

	// Interactive drops into an interactive shell upon build completion or failure.
	Interactive bool
}

// BuildResult contains the outcome of a successful build.
type BuildResult struct {
	Artifacts []string
	OutputDir string
	Container *dagger.Container
}

// BuildGeneric sets up the build container layers, applies raw patches,
// runs Setup and Build scripts, and exports matching artifacts.
func BuildGeneric(ctx context.Context, client *dagger.Client, opts BuildOpts) (*BuildResult, error) {
	if opts.BaseImage == "" {
		return nil, fmt.Errorf("BaseImage must be specified")
	}
	if opts.Build == "" {
		return nil, fmt.Errorf("Build command must be specified for generic builder")
	}
	if opts.Source == nil {
		return nil, fmt.Errorf("Source directory must be provided")
	}

	sourceName := opts.SourceName
	if sourceName == "" {
		sourceName = "src"
	}

	workdir := opts.Workdir
	if workdir == "" {
		workdir = "/work/" + sourceName
	}

	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = "build-output"
	}

	fmt.Printf("[*] Setting up build layer with base image: %s\n", opts.BaseImage)

	// Layer 1: Base image + source directory + workdir
	ctr := client.Container().
		From(opts.BaseImage).
		WithDirectory(workdir, opts.Source).
		WithWorkdir(workdir)

	// Layer 2: Optional raw patch applied directly to the tree
	if opts.RawPatch != "" {
		fmt.Println("[*] Applying extra patch...")
		ctr = ctr.
			WithNewFile("/tmp/shipyard.patch", opts.RawPatch).
			WithExec([]string{"/bin/sh", "-c", "patch -p1 < /tmp/shipyard.patch"})
	}

	// Layer 3: Setup script
	if opts.Setup != "" {
		fmt.Println("[*] Running setup layer...")
		ctr = ctr.WithExec([]string{"/bin/sh", "-c", opts.Setup})
	}

	// Layer 4: Build script
	ctrPreBuild := ctr
	fmt.Println("[*] Running build layer...")
	ctr = ctr.WithExec([]string{"/bin/sh", "-c", opts.Build})

	// Layer 5: Artifact collection & export
	artifactPattern := opts.ArtifactPattern
	if artifactPattern == "" {
		artifactPattern = ".*"
	}
	srcDir := opts.ArtifactDir
	if srcDir == "" {
		srcDir = workdir
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
		return nil, fmt.Errorf("build execution failed: %w", err)
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

// BuildShipfileOpts defines the options for a full shipfile build pipeline.
type BuildShipfileOpts struct {
	// Image overrides the Shipfile's BaseImage.
	Image string

	// Version specifies the target version or tag to build.
	Version string

	// RawPatch is an optional unified diff string applied on top of the shipfile.
	RawPatch string

	// ArtifactPattern overrides the Shipfile's Artifacts regex.
	ArtifactPattern string

	// OutputDir overrides the destination directory for exported artifacts.
	OutputDir string

	// Interactive opens an interactive shell in the container.
	Interactive bool

	// ShipyardPkgDir points to the root directory of the shipyard package (contains shipyard/).
	ShipyardPkgDir string
}

// BuildShipfile coordinates the entire workflow:
//  1. Connect to Dagger
//  2. Pull the pristine source code (from Git or local directory)
//  3. Run the Python interpreter on the source code to apply shipfile CodePatches
//  4. Set up the build container layers (BaseImage, Setup, Build)
//  5. Build the project and export the matching artifacts
func BuildShipfile(ctx context.Context, client *dagger.Client, sf *shipfile.Shipfile, opts BuildShipfileOpts) (*BuildResult, error) {
	image := opts.Image
	if image == "" {
		image = sf.BaseImage
	}
	if image == "" {
		return nil, fmt.Errorf("no base image specified; provide --image or set BaseImage in Shipfile")
	}

	pkgDir := opts.ShipyardPkgDir
	if pkgDir == "" {
		pkgDir = "."
	}

	ver := opts.Version
	if ver == "" {
		ver = "src"
	}
	sourceName := fmt.Sprintf("%s-%s", sf.Name, ver)

	// Step 1: Pull pristine source code
	fmt.Printf("[*] Pulling source code for %s...\n", sf.Name)
	var srcCfg SourceConfig

	// Check if local source directory exists (e.g. sources/<Name>)
	localSource := filepath.Join(sf.Dir, "sources", sf.Name)
	if fi, err := os.Stat(localSource); err == nil && fi.IsDir() {
		srcCfg.LocalDir = localSource
	} else if sf.Url != "" {
		srcCfg.Url = sf.Url
		if opts.Version != "" {
			srcCfg.Tag = opts.Version
		} else if sf.Branch != "" {
			srcCfg.Branch = sf.Branch
		}
	} else {
		// Fallback to shipfile directory itself
		srcCfg.LocalDir = sf.Dir
	}

	pristineSource, err := PullSource(ctx, client, srcCfg)
	if err != nil {
		return nil, fmt.Errorf("pull source: %w", err)
	}

	// Step 2: Run Python interpreter on the source code to apply shipfile CodePatches
	fmt.Printf("[*] Running Python interpreter on source code (%s)...\n", sourceName)
	patchedSource, err := RunShipfile(ctx, client, pristineSource, RunShipfileOpts{
		ShipfileDir:    sf.Dir,
		ShipyardPkgDir: pkgDir,
		SourceName:     sourceName,
		Version:        opts.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("run shipfile python interpreter: %w", err)
	}

	// Step 3: Build using generic builder layers
	artifactPattern := opts.ArtifactPattern
	if artifactPattern == "" {
		artifactPattern = sf.Artifacts
	}

	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = "build-output"
	}

	return BuildGeneric(ctx, client, BuildOpts{
		BaseImage:       image,
		SourceName:      sourceName,
		Source:          patchedSource,
		RawPatch:        opts.RawPatch,
		Setup:           sf.Setup,
		Build:           sf.Build,
		ArtifactPattern: artifactPattern,
		ArtifactDir:     sf.ArtifactDir,
		OutputDir:       outputDir,
		Interactive:     opts.Interactive,
	})
}

