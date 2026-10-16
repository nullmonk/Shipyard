// shipyard-go executes and builds shipfile projects using Dagger and gpython.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"shipyard/pkg/builder"
	"shipyard/pkg/shipfile"
	"shipyard/pkg/shipyard"

	"github.com/go-python/gpython/py"
)

const usage = `Usage: shipyard-go [global flags] <command> [command flags] [args]

Commands:
  build [flags] [shipfile.py | dir]
        Build a project in Dagger layers:
        1. Generic build: shipyard build <shipfile>
           Pulls source, runs python interpreter to patch it, runs setup/build layers,
           and exports artifacts.
        2. Package build: shipyard build --package <image> <shipfile>
           Spins up distro container, installs build deps, snapshots pristine source,
           calls python interpreter to patch it, diffs the patch, resets tree, applies
           the patch the distro's way, builds in the target container, and extracts artifacts.

        Flags:
          -package      Distro container image for package build (e.g. ubuntu:20.04, debian:bookworm)
          -image        Base container image for generic build (e.g. golang:1.22-alpine)
          -version      Version/tag of source code to build
          -output       Output directory for artifacts (default "build-output")
          -artifacts    Regex pattern for filtering exported artifacts
          -patch        Extra unified patch file (.patch) to apply
          -interactive  Drop into interactive container shell on finish/error
          -remote       Remote Dagger engine runner endpoint
          -host         Run phases directly on host with gpython (legacy mode)
          -phase        Host phase to run: "setup" | "build" | "all" (default "all")

  validate <shipfile.py | dir>
        Validate that a shipfile satisfies source, patcher, and builder requirements.

  info  <shipfile.py | dir>
        Print Name, Url, VersionTags, BaseImage, and other metadata.

  run   <shipfile.py | dir>
        Execute a shipfile and print its Shipfile.Name.

Global Flags:
  -shipyard-pkg <dir>  Path to shipyard package root (default: auto-detected)
`

func main() {
	globalFlags := flag.NewFlagSet("shipyard", flag.ExitOnError)
	pkgDir := globalFlags.String("shipyard-pkg", defaultShipyardPkg(), "directory containing the shipyard Python package")
	globalFlags.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
	}

	if len(os.Args) < 2 {
		globalFlags.Usage()
		os.Exit(1)
	}

	// Parse global flags if any before subcommand
	_ = globalFlags.Parse(os.Args[1:])
	args := globalFlags.Args()
	if len(args) == 0 {
		globalFlags.Usage()
		os.Exit(1)
	}

	cmd := args[0]
	cmdArgs := args[1:]

	interp, err := shipyard.New(*pkgDir)
	if err != nil {
		fatalf("init interpreter: %v", err)
	}
	defer interp.Close()

	switch cmd {
	case "validate":
		target := "."
		if len(cmdArgs) > 0 {
			target = cmdArgs[0]
		}
		if err := cmdValidate(interp, target); err != nil {
			fatalf("%v", err)
		}
	case "build":
		if err := runCmdBuild(interp, *pkgDir, cmdArgs); err != nil {
			fatalf("%v", err)
		}
	case "info":
		target := "."
		if len(cmdArgs) > 0 {
			target = cmdArgs[0]
		}
		if err := cmdInfo(interp, target); err != nil {
			fatalf("%v", err)
		}
	case "run":
		target := "."
		if len(cmdArgs) > 0 {
			target = cmdArgs[0]
		}
		if err := cmdRun(interp, target); err != nil {
			fatalf("%v", err)
		}
	default:
		fatalf("unknown command %q", cmd)
	}
}

func runCmdBuild(interp *shipyard.Interpreter, pkgDir string, args []string) error {
	buildFlags := flag.NewFlagSet("build", flag.ExitOnError)
	packageImage := buildFlags.String("package", "", "distro container image for package build (e.g. ubuntu:20.04, debian:bookworm)")
	packageName := buildFlags.String("package-name", "", "override package name (defaults to Shipfile.PackageName or Name)")
	image := buildFlags.String("image", "", "base container image")
	version := buildFlags.String("version", "", "target version/tag")
	output := buildFlags.String("output", "", "output directory for artifacts (default: build-output)")
	artifacts := buildFlags.String("artifacts", "", "artifact regex pattern")
	patchPath := buildFlags.String("patch", "", "extra raw .patch file")
	interactive := buildFlags.Bool("interactive", false, "drop into interactive shell")
	remote := buildFlags.String("remote", "", "remote dagger engine endpoint")
	hostMode := buildFlags.Bool("host", false, "run phases directly on host via gpython")
	phase := buildFlags.String("phase", "all", "host build phase (setup, build, all)")

	if err := buildFlags.Parse(args); err != nil {
		return err
	}

	if buildFlags.NArg() < 1 {
		return fmt.Errorf("shipyard build requires a <shipfile> argument\n\nUsage:\n  shipyard build <shipfile>\n  shipyard build --package <image> <shipfile>")
	}
	if buildFlags.NArg() > 1 {
		return fmt.Errorf("unexpected extra arguments: %v\n\nUsage:\n  shipyard build <shipfile>\n  shipyard build --package <image> <shipfile>", buildFlags.Args()[1:])
	}

	target := buildFlags.Arg(0)
	sfPath, err := shipfile.Find(target)
	if err != nil {
		return fmt.Errorf("find shipfile %s: %w", target, err)
	}

	if *hostMode {
		return cmdBuildHost(interp, sfPath, *version, *phase)
	}

	sf, err := shipfile.Load(sfPath, interp)
	if err != nil {
		return fmt.Errorf("load shipfile %s: %w", sfPath, err)
	}

	if err := sf.Validate(); err != nil {
		return fmt.Errorf("validate shipfile %s: %w", sfPath, err)
	}

	// Validate shipfile type against the requested build mode
	if *packageImage != "" {
		if !sf.IsPatch() {
			return fmt.Errorf("cannot run package build: %s does not subclass 'Patcher' (did you mean 'shipyard build %s'?)", sf.Path, target)
		}
	} else {
		if !sf.IsBuild() {
			return fmt.Errorf("cannot run standalone build: %s does not subclass 'Builder' (did you mean 'shipyard build --package <image> %s'?)", sf.Path, target)
		}
	}

	rawPatchContent := ""
	if *patchPath != "" {
		data, err := os.ReadFile(*patchPath)
		if err != nil {
			return fmt.Errorf("read patch file %s: %w", *patchPath, err)
		}
		rawPatchContent = string(data)
	}

	ctx := context.Background()
	engine, err := builder.Connect(ctx, *remote)
	if err != nil {
		return fmt.Errorf("connect to Dagger: %w", err)
	}
	defer engine.Close()

	if *packageImage != "" {
		outDir := *output
		if outDir == "" {
			cleanImg := strings.ReplaceAll(*packageImage, ":", "/")
			outDir = filepath.Join("build-output", cleanImg)
		}
		pkgOpts := builder.BuildPackageOpts{
			PackageName:     *packageName,
			Version:         *version,
			RawPatch:        rawPatchContent,
			ArtifactPattern: *artifacts,
			OutputDir:       outDir,
			Interactive:     *interactive,
			ShipyardPkgDir:  pkgDir,
		}
		res, err := builder.BuildPackage(ctx, engine.Client(), sf, *packageImage, pkgOpts)
		if err != nil {
			return fmt.Errorf("package build failed: %w", err)
		}
		fmt.Printf("[+] Package build completed successfully with %d artifacts in %s\n", len(res.Artifacts), res.OutputDir)
		return nil
	}

	outDir := *output
	if outDir == "" {
		outDir = "build-output"
	}

	opts := builder.BuildShipfileOpts{
		Image:           *image,
		Version:         *version,
		RawPatch:        rawPatchContent,
		ArtifactPattern: *artifacts,
		OutputDir:       outDir,
		Interactive:     *interactive,
		ShipyardPkgDir:  pkgDir,
	}

	res, err := builder.BuildShipfile(ctx, engine.Client(), sf, opts)
	if err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	fmt.Printf("[+] Build completed successfully with %d artifacts in %s\n", len(res.Artifacts), res.OutputDir)
	return nil
}

func cmdRun(interp *shipyard.Interpreter, path string) error {
	sfPath, err := shipfile.Find(path)
	if err != nil {
		return err
	}
	mod, err := interp.RunShipfile(sfPath)
	if err != nil {
		py.TracebackDump(err)
		return fmt.Errorf("run %s: %v", sfPath, err)
	}
	name := moduleAttr(mod, "Shipfile", "Name")
	fmt.Printf("ran %s (Name=%s)\n", sfPath, name)
	return nil
}

func cmdValidate(interp *shipyard.Interpreter, path string) error {
	sfPath, err := shipfile.Find(path)
	if err != nil {
		return err
	}
	sf, err := shipfile.Load(sfPath, interp)
	if err != nil {
		return fmt.Errorf("load shipfile %s: %w", sfPath, err)
	}
	if err := sf.Validate(); err != nil {
		return fmt.Errorf("shipfile %s is invalid:\n%w", sfPath, err)
	}
	fmt.Printf("Shipfile %s is valid.\n", sfPath)
	return nil
}

func cmdInfo(interp *shipyard.Interpreter, path string) error {
	sfPath, err := shipfile.Find(path)
	if err != nil {
		return err
	}
	sf, err := shipfile.Load(sfPath, interp)
	if err != nil {
		return err
	}

	fmt.Printf("Shipfile: %s\n", sf.Path)
	printAttr("Name", sf.Name)
	printAttr("Url", sf.Url)
	if len(sf.Urls) > 0 {
		printAttr("Urls", fmt.Sprintf("%v", sf.Urls))
	}
	printAttr("VersionTags", sf.VersionTags)
	printAttr("PackageName", sf.PackageName)
	printAttr("BaseImage", sf.BaseImage)
	printAttr("Setup", sf.Setup)
	printAttr("Build", sf.Build)
	printAttr("Artifacts", sf.Artifacts)
	printAttr("ArtifactDir", sf.ArtifactDir)
	return nil
}

func printAttr(key, val string) {
	if val != "" {
		fmt.Printf("  %-14s %s\n", key+":", val)
	}
}

// cmdBuildHost runs the shipfile locally (no Dagger) by calling apply_patches
// then run_build on the Shipfile instance, passing sourceDir and version.
// phase gates which runners are called: "patch", "build", or "all" (default).
func cmdBuildHost(interp *shipyard.Interpreter, path, version, phase string) error {
	mod, err := interp.RunShipfile(path)
	if err != nil {
		py.TracebackDump(err)
		return fmt.Errorf("load %s: %v", path, err)
	}

	sfClass, ok := mod.Globals["Shipfile"]
	if !ok {
		return fmt.Errorf("no Shipfile class found in %s", path)
	}

	sfInst, err := py.Call(sfClass, py.Tuple{}, nil)
	if err != nil {
		return fmt.Errorf("instantiate Shipfile: %v", err)
	}

	interp.SetFileRead(true)
	if err := interp.EnableRun(); err != nil {
		return fmt.Errorf("enable Run: %v", err)
	}
	defer func() {
		interp.SetFileRead(false)
		if err := interp.DisableRun(); err != nil {
			fmt.Fprintf(os.Stderr, "shipyard-go: warning: DisableRun: %v\n", err)
		}
	}()

	// Resolve source directory from the Shipfile instance.
	sourceDir, err := resolveSourceDir(sfInst, path)
	if err != nil {
		return err
	}

	vArg := py.String(version)
	sdArg := py.String(sourceDir)

	type runner struct {
		method string
		phases []string // which -phase values trigger this runner
	}
	runners := []runner{
		{"apply_patches", []string{"patch", "all"}},
		{"run_build", []string{"build", "all"}},
	}

	for _, r := range runners {
		if !containsPhase(r.phases, phase) {
			continue
		}
		method, err := py.GetAttrString(sfInst, r.method)
		if err != nil {
			continue // Shipfile doesn't implement this runner
		}
		fmt.Printf("[*] %s (source=%s version=%s)\n", r.method, sourceDir, version)
		if _, err := py.Call(method, py.Tuple{sdArg, vArg}, nil); err != nil {
			py.TracebackDump(err)
			return fmt.Errorf("%s failed: %v", r.method, err)
		}
		fmt.Printf("[+] %s done\n", r.method)
	}
	return nil
}

// resolveSourceDir returns the source_directory() value from the Shipfile
// instance, falling back to "sources/<Name>" next to the shipfile.
func resolveSourceDir(sfInst py.Object, sfPath string) (string, error) {
	if fn, err := py.GetAttrString(sfInst, "source_directory"); err == nil {
		if result, err := py.Call(fn, py.Tuple{}, nil); err == nil {
			if s, ok := result.(py.String); ok && string(s) != "" {
				return string(s), nil
			}
		}
	}
	name, err := py.GetAttrString(sfInst, "Name")
	if err != nil {
		return "", fmt.Errorf("Shipfile has no Name: %v", err)
	}
	n, _ := name.(py.String)
	return filepath.Join(filepath.Dir(sfPath), "sources", string(n)), nil
}

func containsPhase(phases []string, phase string) bool {
	if phase == "" {
		phase = "all"
	}
	for _, p := range phases {
		if p == phase {
			return true
		}
	}
	return false
}

func moduleAttr(mod *py.Module, class, attr string) string {
	sfClass, ok := mod.Globals[class]
	if !ok {
		return ""
	}
	val, err := py.GetAttrString(sfClass, attr)
	if err != nil {
		return ""
	}
	if s, ok := val.(py.String); ok {
		return string(s)
	}
	return ""
}

func defaultShipyardPkg() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 5; i++ {
			if fi, err := os.Stat(filepath.Join(dir, "shipyard", "__init__.py")); err == nil && !fi.IsDir() {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if fi, err := os.Stat(filepath.Join("shipyard", "__init__.py")); err == nil && !fi.IsDir() {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
	}
	return "."
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "shipyard-go: "+format+"\n", a...)
	os.Exit(1)
}
