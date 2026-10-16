package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"
)

const (
	// DefaultRunnerImage is the container image used to execute Python shipfiles.
	DefaultRunnerImage = "python:3-slim"
)

// RunShipfileOpts configures how the Python shipfile is executed on the source.
type RunShipfileOpts struct {
	// ShipfileDir is the host path containing the shipfile.py.
	ShipfileDir string

	// ShipyardPkgDir is the host path containing the shipyard Python package (parent of shipyard/).
	ShipyardPkgDir string

	// SourceName is the name of the source directory (e.g. "tool-1.0").
	SourceName string

	// Version is an optional version string passed to shipyard.runner.
	Version string

	// RunnerImage is the Python container image to use (defaults to python:3-slim).
	RunnerImage string

	// Env specifies additional environment variables for the runner container.
	Env map[string]string
}

// RunShipfile sets up a Python container layer and executes the shipfile against
// the source directory, returning the patched source *dagger.Directory.
func RunShipfile(ctx context.Context, client *dagger.Client, source *dagger.Directory, opts RunShipfileOpts) (*dagger.Directory, error) {
	if opts.ShipfileDir == "" {
		return nil, fmt.Errorf("ShipfileDir must be specified")
	}
	if opts.ShipyardPkgDir == "" {
		return nil, fmt.Errorf("ShipyardPkgDir must be specified")
	}
	if opts.SourceName == "" {
		opts.SourceName = "source"
	}
	image := opts.RunnerImage
	if image == "" {
		image = DefaultRunnerImage
	}

	shipfileAbs, err := filepath.Abs(opts.ShipfileDir)
	if err != nil {
		return nil, fmt.Errorf("resolve shipfile dir: %w", err)
	}
	shipyardPkgAbs, err := filepath.Abs(opts.ShipyardPkgDir)
	if err != nil {
		return nil, fmt.Errorf("resolve shipyard pkg dir: %w", err)
	}

	// Mount shipfile directory (excluding caches and sources)
	shipfileHostDir := client.Host().Directory(shipfileAbs, dagger.HostDirectoryOpts{
		Exclude: []string{"sources/", "__pycache__/", "build-output/"},
	})

	// Resolve shipyard python package directory
	shipyardPkgPath := filepath.Join(shipyardPkgAbs, "shipyard")
	if fi, err := os.Stat(filepath.Join(shipyardPkgAbs, "__init__.py")); err == nil && !fi.IsDir() {
		shipyardPkgPath = shipyardPkgAbs
	}

	shipyardHostDir := client.Host().Directory(shipyardPkgPath, dagger.HostDirectoryOpts{
		Exclude: []string{"__pycache__/", ".git/"},
	})

	workPath := "/work/" + opts.SourceName
	args := []string{"python", "-m", "shipyard.runner", "/ship", workPath}
	if opts.Version != "" {
		args = append(args, opts.Version)
	}

	ctr := client.Container().From(image).
		WithDirectory("/opt/shipyard/shipyard", shipyardHostDir).
		WithEnvVariable("PYTHONPATH", "/opt/shipyard").
		WithEnvVariable("PYTHONDONTWRITEBYTECODE", "1").
		WithDirectory("/ship", shipfileHostDir).
		WithDirectory(workPath, source)

	// Also mount shipfile python package if present in the repo
	repoRoot := filepath.Dir(shipyardPkgPath)
	shipfilePkgPath := filepath.Join(repoRoot, "shipfile")
	if fi, err := os.Stat(filepath.Join(shipfilePkgPath, "__init__.py")); err == nil && !fi.IsDir() {
		ctr = ctr.WithDirectory("/opt/shipyard/shipfile", client.Host().Directory(shipfilePkgPath, dagger.HostDirectoryOpts{
			Exclude: []string{"__pycache__/", ".git/"},
		}))
	}

	for k, v := range opts.Env {
		ctr = ctr.WithEnvVariable(k, v)
	}

	ctr = ctr.WithExec(args)

	return ctr.Directory(workPath), nil
}

// RunPython runs an inline Python script or command inside a container layer
// against the provided source directory and returns the resulting directory and stdout.
func RunPython(ctx context.Context, client *dagger.Client, source *dagger.Directory, workdir, script string, args []string, runnerImage string) (*dagger.Directory, string, error) {
	if runnerImage == "" {
		runnerImage = DefaultRunnerImage
	}
	if workdir == "" {
		workdir = "/work"
	}

	ctr := client.Container().From(runnerImage).
		WithDirectory(workdir, source).
		WithWorkdir(workdir)

	execArgs := []string{"python", "-c", script}
	execArgs = append(execArgs, args...)
	ctr = ctr.WithExec(execArgs)

	stdout, err := ctr.Stdout(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("python script failed: %w", err)
	}

	return ctr.Directory(workdir), stdout, nil
}

