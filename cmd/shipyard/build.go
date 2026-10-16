package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-python/gpython/py"
	"github.com/spf13/cobra"

	"shipyard/pkg/builder"
	"shipyard/pkg/shipfile"
	"shipyard/pkg/shipyard"
)

var (
	buildPackageImage string
	buildPackageName  string
	buildImage        string
	buildVersion      string
	buildOutput       string
	buildArtifacts    string
	buildPatchPath    string
	buildInteractive  bool
	buildLocal        bool
	buildHostMode     bool
	buildPhase        string
)

var buildCmd = &cobra.Command{
	Use:   "build [flags] [shipfile]",
	Short: "Build a generic project or distro package",
	Long: `Build executes a shipfile using Dagger container layers or local execution.
If --local is set, it runs setup and build directly in the current directory without Dagger.
If --package is provided, spins up a distro container to create an OS package.
Otherwise, executes a generic build in the base container image.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runBuild,
}

func init() {
	buildCmd.Flags().BoolVarP(&buildLocal, "local", "l", false, "run setup and build in current directory without Dagger")
	buildCmd.Flags().StringVarP(&buildPackageImage, "package", "p", "", "distro container image for package build (e.g. ubuntu:22.04, debian:bookworm)")
	buildCmd.Flags().StringVar(&buildPackageName, "package-name", "", "override package name (defaults to Shipfile PackageName or Name)")
	buildCmd.Flags().StringVarP(&buildImage, "image", "i", "", "base container image for generic build (defaults to Shipfile BaseImage)")
	buildCmd.Flags().StringVarP(&buildVersion, "version", "v", "", "target version/tag to build")
	buildCmd.Flags().StringVarP(&buildOutput, "output", "o", "", "output directory for artifacts (default: build-output)")
	buildCmd.Flags().StringVarP(&buildArtifacts, "artifacts", "a", "", "artifact regex pattern")
	buildCmd.Flags().StringVar(&buildPatchPath, "patch", "", "extra raw .patch file to apply")
	buildCmd.Flags().BoolVar(&buildInteractive, "interactive", false, "drop into interactive container shell on finish/error")
	buildCmd.Flags().BoolVar(&buildHostMode, "host", false, "run phases directly on host via gpython (legacy mode)")
	buildCmd.Flags().StringVar(&buildPhase, "phase", "all", "build phase to run (setup, build, all)")
}

func runBuild(cmd *cobra.Command, args []string) error {
	target := ""
	if len(args) > 0 {
		target = args[0]
	}

	sfPath, err := resolveShipfilePath(target)
	if err != nil {
		return err
	}

	interp, err := getInterpreter()
	if err != nil {
		return fmt.Errorf("init interpreter: %w", err)
	}
	defer interp.Close()

	if buildHostMode {
		return cmdBuildHost(interp, sfPath, buildVersion, buildPhase)
	}

	sf, err := shipfile.Load(sfPath, interp)
	if err != nil {
		return fmt.Errorf("load shipfile %s: %w", sfPath, err)
	}

	if err := sf.Validate(); err != nil {
		return fmt.Errorf("validate shipfile %s: %w", sfPath, err)
	}

	if buildLocal {
		return runLocalBuild(sf, interp, buildOutput, buildArtifacts, buildPhase)
	}

	if buildPackageImage != "" {
		if !sf.IsPatch() {
			return fmt.Errorf("cannot run package build: %s does not subclass 'Patcher'", sf.Path)
		}
	} else {
		if !sf.IsBuild() {
			return fmt.Errorf("cannot run generic build: %s does not subclass 'Builder' (did you mean '--package <image>'?)", sf.Path)
		}
	}

	rawPatchContent := ""
	if buildPatchPath != "" {
		data, err := os.ReadFile(buildPatchPath)
		if err != nil {
			return fmt.Errorf("read patch file %s: %w", buildPatchPath, err)
		}
		rawPatchContent = string(data)
	}

	ctx := context.Background()
	engine, err := builder.Connect(ctx, globalRemote)
	if err != nil {
		return fmt.Errorf("connect to Dagger: %w", err)
	}
	defer engine.Close()

	if buildPackageImage != "" {
		outDir := buildOutput
		if outDir == "" {
			cleanImg := strings.ReplaceAll(buildPackageImage, ":", "/")
			outDir = filepath.Join("build-output", cleanImg)
		}
		pkgOpts := builder.BuildPackageOpts{
			PackageName:     buildPackageName,
			Version:         buildVersion,
			RawPatch:        rawPatchContent,
			ArtifactPattern: buildArtifacts,
			OutputDir:       outDir,
			Interactive:     buildInteractive,
			ShipyardPkgDir:  globalPkgDir,
		}
		res, err := builder.BuildPackage(ctx, engine.Client(), sf, buildPackageImage, pkgOpts)
		if err != nil {
			return fmt.Errorf("package build failed: %w", err)
		}
		fmt.Printf("[+] Package build completed successfully with %d artifacts in %s\n", len(res.Artifacts), res.OutputDir)
		return nil
	}

	outDir := buildOutput
	if outDir == "" {
		outDir = "build-output"
	}

	opts := builder.BuildShipfileOpts{
		Image:           buildImage,
		Version:         buildVersion,
		RawPatch:        rawPatchContent,
		ArtifactPattern: buildArtifacts,
		OutputDir:       outDir,
		Interactive:     buildInteractive,
		ShipyardPkgDir:  globalPkgDir,
	}

	res, err := builder.BuildShipfile(ctx, engine.Client(), sf, opts)
	if err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	fmt.Printf("[+] Build completed successfully with %d artifacts in %s\n", len(res.Artifacts), res.OutputDir)
	return nil
}

func runLocalBuild(sf *shipfile.Shipfile, interp *shipyard.Interpreter, outDir, artifactPattern, phase string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current working directory: %w", err)
	}

	fmt.Printf("[*] Running local build in %s\n", cwd)
	fmt.Printf("    Shipfile: %s\n", sf.Path)

	mod, err := interp.RunShipfile(sf.Path)
	if err != nil {
		return fmt.Errorf("run shipfile %s: %w", sf.Path, err)
	}

	var sfInst py.Object
	if sfClass, ok := mod.Globals["Shipfile"]; ok {
		if inst, err := py.Call(sfClass, py.Tuple{}, nil); err == nil {
			sfInst = inst
		}
	}

	shouldRunSetup := containsPhase([]string{"setup", "all"}, phase)
	shouldRunBuild := containsPhase([]string{"build", "all"}, phase)

	interp.SetFileRead(true)
	if err := interp.EnableRun(); err != nil {
		return fmt.Errorf("enable Run: %w", err)
	}
	defer func() {
		interp.SetFileRead(false)
		_ = interp.DisableRun()
	}()

	if shouldRunSetup {
		if sf.HasSetup && sfInst != nil {
			fmt.Println("[*] Running setup()...")
			if err := callMethod(sfInst, "setup"); err != nil {
				return err
			}
			fmt.Println("[+] Setup completed")
		} else if sf.Setup != "" {
			fmt.Printf("[*] Running setup: %s\n", sf.Setup)
			cmd := exec.Command("/bin/sh", "-c", sf.Setup)
			cmd.Dir = cwd
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("setup failed: %w", err)
			}
			fmt.Println("[+] Setup completed")
		}
	}

	if shouldRunBuild {
		if sf.HasBuild && sfInst != nil {
			fmt.Println("[*] Running build()...")
			if err := callMethod(sfInst, "build"); err != nil {
				return err
			}
			fmt.Println("[+] Build completed")
		} else if sf.Build != "" {
			fmt.Printf("[*] Running build: %s\n", sf.Build)
			cmd := exec.Command("/bin/sh", "-c", sf.Build)
			cmd.Dir = cwd
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("build failed: %w", err)
			}
			fmt.Println("[+] Build completed")
		}
	}

	pattern := artifactPattern
	if pattern == "" {
		pattern = sf.Artifacts
	}

	if pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("invalid artifact regex %q: %w", pattern, err)
		}

		searchDir := cwd
		if sf.ArtifactDir != "" {
			if filepath.IsAbs(sf.ArtifactDir) {
				searchDir = sf.ArtifactDir
			} else {
				searchDir = filepath.Join(cwd, sf.ArtifactDir)
			}
		}

		var found []string
		entries, err := os.ReadDir(searchDir)
		if err == nil {
			for _, e := range entries {
				if re.MatchString(e.Name()) {
					found = append(found, filepath.Join(searchDir, e.Name()))
				}
			}
		}

		if len(found) > 0 {
			fmt.Printf("[+] Found %d artifacts matching %q in %s:\n", len(found), pattern, searchDir)
			for _, art := range found {
				fmt.Printf("    * %s\n", art)
			}
			if outDir != "" && outDir != searchDir {
				if err := os.MkdirAll(outDir, 0755); err != nil {
					return fmt.Errorf("create output directory %s: %w", outDir, err)
				}
				for _, src := range found {
					dst := filepath.Join(outDir, filepath.Base(src))
					if err := copyFile(src, dst); err != nil {
						return fmt.Errorf("copy artifact %s: %w", src, err)
					}
					fmt.Printf("    -> %s\n", dst)
				}
			}
		} else {
			fmt.Printf("[-] No artifacts matching %q found in %s\n", pattern, searchDir)
		}
	}

	fmt.Println("[+] Local build completed successfully")
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	fi, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

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
			fmt.Fprintf(os.Stderr, "shipyard: warning: DisableRun: %v\n", err)
		}
	}()

	sourceDir, err := resolveSourceDir(sfInst, path)
	if err != nil {
		return err
	}

	vArg := py.String(version)
	sdArg := py.String(sourceDir)

	type runner struct {
		method string
		phases []string
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
			continue
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

func callMethod(inst py.Object, methodName string) error {
	fn, err := py.GetAttrString(inst, methodName)
	if err != nil {
		return fmt.Errorf("no method %s found on Shipfile instance: %w", methodName, err)
	}
	// Try calling with no args first: def method(self)
	_, err = py.Call(fn, py.Tuple{}, nil)
	if err == nil {
		return nil
	}
	// If TypeError (e.g. requires ctx argument: def method(self, ctx)), retry with (None,)
	if _, ok := err.(*py.Exception); ok {
		if _, err2 := py.Call(fn, py.Tuple{py.None}, nil); err2 == nil {
			return nil
		}
	}
	py.TracebackDump(err)
	return fmt.Errorf("%s() failed: %v", methodName, err)
}


