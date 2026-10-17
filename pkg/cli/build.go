package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"shipyard/pkg/builder"
	"shipyard/pkg/shipfile"
)

var (
	buildName         string
	buildPackageImage string
	buildPackageName  string
	buildImage        string
	buildSystem       string
	buildVersion      string
	buildOutput       string
	buildArtifacts    string
	buildInteractive  bool
	buildLocal        bool
	buildNoRemote     bool
	buildNoStream     bool
	buildCopy         string
	buildNoTest       bool
)

func isDaggerRunnerURI(s string) bool {
	for _, p := range []string{"tcp://", "unix://", "docker-container://", "docker-image://", "kube-pod://", "ssh://"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func newBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [flags] [shipfile]",
		Short: "Build a generic project or distro package",
		Long: `Build executes a shipfile.

If a remote server is active (via 'shipyard remote connect' or 'shipyard remote use <name>'),
the build runs on the remote server automatically (use 'shipyard remote use local' or '--no-remote' to build locally).
Without --package: runs a generic build in a container (BaseImage).
With --package <image>: spins up a distro container to create an OS package.
With --local: runs setup() and build() directly on the host without Dagger.`,
		Args: func(cmd *cobra.Command, args []string) error {
			max := 1
			if cmd.Flags().Changed("copy") {
				max = 2
			}
			if len(args) > max {
				return fmt.Errorf("accepts at most %d arg(s), received %d", max, len(args))
			}
			return nil
		},
		RunE: runBuild,
	}
	cmd.Flags().BoolVarP(&buildLocal, "local", "l", false, "run setup() and build() directly on host without Dagger")
	cmd.Flags().BoolVar(&buildNoRemote, "no-remote", false, "run container build locally with Dagger even if a remote profile is active")
	cmd.Flags().StringVar(&buildName, "name", "", "override tool name when building on a remote server")
	cmd.Flags().StringVarP(&buildPackageImage, "package", "p", "", "distro container image for package build (e.g. ubuntu:22.04)")
	cmd.Flags().StringVar(&buildPackageName, "package-name", "", "override package name (defaults to Shipfile PackageName or Name)")
	cmd.Flags().StringVarP(&buildImage, "image", "i", "", "base container image for generic build (defaults to Shipfile BaseImage)")
	cmd.Flags().StringVarP(&buildSystem, "system", "s", "", "target tracked system name or image on remote server")
	cmd.Flags().StringVarP(&buildVersion, "version", "v", "", "target version/tag to build ($SHIPYARD_VERSION)")
	cmd.Flags().StringVarP(&buildOutput, "output", "o", "", "output directory for artifacts (default: build-output)")
	cmd.Flags().StringVarP(&buildArtifacts, "artifacts", "a", "", "artifact regex pattern")
	cmd.Flags().BoolVar(&buildInteractive, "interactive", false, "drop into interactive container shell on error")
	cmd.Flags().BoolVar(&buildNoStream, "no-stream", false, "trigger remote build asynchronously without streaming output")
	cmd.Flags().StringVarP(&buildCopy, "copy", "c", "", "copy local directory (or specified path) into container instead of git")
	cmd.Flags().Lookup("copy").NoOptDefVal = "."
	cmd.Flags().BoolVar(&buildNoTest, "no-test", false, "skip running tests defined in shipfile")
	return cmd
}

func runBuild(cmd *cobra.Command, args []string) error {
	target := ""
	copyDir := ""

	if cmd.Flags().Changed("copy") {
		copyDir = buildCopy
		if copyDir == "." {
			if len(args) == 2 {
				copyDir = args[0]
				target = args[1]
			} else if len(args) == 1 {
				abs, err := filepath.Abs(args[0])
				if err == nil {
					fi, statErr := os.Stat(abs)
					if statErr == nil && fi.IsDir() {
						candidate := filepath.Join(abs, "shipfile.py")
						if _, err := os.Stat(candidate); err != nil {
							copyDir = args[0]
						} else {
							target = args[0]
						}
					} else {
						target = args[0]
					}
				} else {
					target = args[0]
				}
			}
		} else {
			if len(args) > 0 {
				target = args[0]
			}
		}
	} else if len(args) > 0 {
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

	sf, err := shipfile.Load(sfPath, interp)
	if err != nil {
		return fmt.Errorf("load shipfile %s: %w", sfPath, err)
	}
	if err := sf.Validate(); err != nil {
		return fmt.Errorf("shipfile %s is invalid: %w", sfPath, err)
	}

	if cmd.Flags().Changed("copy") {
		if copyDir == "." || copyDir == "" {
			copyDir = sf.Dir
		} else {
			absCopy, err := filepath.Abs(copyDir)
			if err != nil {
				return fmt.Errorf("resolve copy path %s: %w", copyDir, err)
			}
			fi, err := os.Stat(absCopy)
			if err != nil || !fi.IsDir() {
				return fmt.Errorf("copy source directory does not exist: %s", absCopy)
			}
			copyDir = absCopy
		}
	}

	version := buildVersion
	if version == "" {
		version = os.Getenv("SHIPYARD_VERSION")
	}

	if buildLocal {
		if err := runLocalBuild(sf, interp, version, buildOutput, buildArtifacts); err != nil {
			return err
		}
		return runTests(sf, version)
	}

	if buildPackageImage != "" || buildSystem != "" {
		if !sf.IsPatch() {
			return fmt.Errorf("cannot run package build: %s does not subclass 'Patcher'", sf.Path)
		}
	} else {
		if !sf.IsBuild() {
			return fmt.Errorf("cannot run generic build: %s does not subclass 'Builder' (did you mean '--package <image>'?)", sf.Path)
		}
	}

	if !buildNoRemote && !isDaggerRunnerURI(globalRemote) {
		if baseURL, tok, rErr := resolveRemoteEndpoint("", ""); rErr == nil && baseURL != "" {
			raw, readErr := os.ReadFile(sfPath)
			if readErr != nil {
				return fmt.Errorf("read %s: %w", sfPath, readErr)
			}
			var verbosePtr *bool
			if cmd.Flags().Changed("verbose") || globalVerbose {
				v := globalVerbose
				verbosePtr = &v
			}
			reqBody := remoteBuildRequest{
				Name:            buildName,
				Shipfile:        string(raw),
				System:          buildSystem,
				PackageImage:    buildPackageImage,
				PackageName:     buildPackageName,
				Image:           buildImage,
				Version:         version,
				ArtifactPattern: buildArtifacts,
				NoTest:          buildNoTest,
				Verbose:         verbosePtr,
				Stream:          !buildNoStream || buildInteractive,
				Interactive:     buildInteractive,
			}
			fmt.Printf("[*] Building on remote %s...\n", baseURL)
			return executeRemoteBuildStream(baseURL, tok, reqBody)
		}
	}

	daggerRemote := ""
	if isDaggerRunnerURI(globalRemote) {
		daggerRemote = globalRemote
	}

	ctx := context.Background()
	engine, err := builder.Connect(ctx, daggerRemote, globalVerbose)
	if err != nil {
		return fmt.Errorf("connect to Dagger: %w", err)
	}
	defer engine.Close()

	// In-container command stdout always goes to the terminal.
	// Setup/progress messages (image pull, deps install, etc.) are suppressed unless --verbose.
	// Build services redirect by setting Output/Stdout/Stderr on the opts directly.
	progressOut := io.Writer(io.Discard)
	if globalVerbose {
		progressOut = os.Stdout
	}

	if buildPackageImage != "" {
		outDir := buildOutput
		if outDir == "" {
			cleanImg := strings.ReplaceAll(buildPackageImage, ":", "/")
			outDir = filepath.Join("build-output", cleanImg)
		}
		res, err := builder.BuildPackage(ctx, engine.Client(), sf, buildPackageImage, builder.BuildPackageOpts{
			PackageName:     buildPackageName,
			Version:         version,
			ArtifactPattern: buildArtifacts,
			OutputDir:       outDir,
			Interactive:     buildInteractive,
			NoTest:          buildNoTest,
			Verbose:         globalVerbose,
			Output:          progressOut,
			Stdout:          os.Stdout,
			Stderr:          os.Stderr,
			Copy:            copyDir,
		})
		if err != nil {
			return fmt.Errorf("package build failed: %w", err)
		}
		fmt.Printf("[+] Package build completed: %d artifacts in %s\n", len(res.Artifacts), res.OutputDir)
		return nil
	}

	outDir := buildOutput
	if outDir == "" {
		outDir = filepath.Join("build-output", "standalone", sf.Name)
	}
	res, err := builder.BuildShipfile(ctx, engine.Client(), sf, builder.BuildShipfileOpts{
		Image:           buildImage,
		Version:         version,
		ArtifactPattern: buildArtifacts,
		OutputDir:       outDir,
		Interactive:     buildInteractive,
		NoTest:          buildNoTest,
		Verbose:         globalVerbose,
		Output:          progressOut,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		Copy:            copyDir,
	})
	if err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Printf("[+] Build completed: %d artifacts in %s\n", len(res.Artifacts), res.OutputDir)
	return nil
}

func runTests(sf *shipfile.Shipfile, version string) error {
	if !sf.IsTest() || buildNoTest {
		return nil
	}
	fmt.Println("[*] Running tests...")
	results, err := sf.RunTests(shipfile.RunContext{Version: version})
	if err != nil {
		return fmt.Errorf("test runner failed: %w", err)
	}
	failed := 0
	for _, r := range results {
		label := r.Name
		if label == "" {
			label = "(unnamed)"
		}
		if r.Passed {
			fmt.Printf("[+] PASS  %s\n", label)
		} else {
			fmt.Printf("[-] FAIL  %s: %s\n", label, r.Error)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d test(s) failed", failed)
	}
	fmt.Printf("[+] All %d test(s) passed\n", len(results))
	return nil
}

func runLocalBuild(sf *shipfile.Shipfile, interp *shipfile.Interpreter, version, outDir, artifactPattern string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	fmt.Printf("[*] Running local build in %s\n", cwd)
	if err := sf.Build(shipfile.RunContext{SourceDir: cwd, Version: version}); err != nil {
		return err
	}
	if artifactPattern == "" {
		artifactPattern = sf.Artifacts
	}
	if artifactPattern != "" {
		searchDir := cwd
		if sf.ArtifactDir != "" {
			if filepath.IsAbs(sf.ArtifactDir) {
				searchDir = sf.ArtifactDir
			} else {
				searchDir = filepath.Join(cwd, sf.ArtifactDir)
			}
		}
		printLocalArtifacts(searchDir, artifactPattern, outDir)
	}
	fmt.Println("[+] Local build completed")
	return nil
}

func printLocalArtifacts(searchDir, pattern, outDir string) {
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if matchPattern(e.Name(), pattern) {
			src := filepath.Join(searchDir, e.Name())
			fmt.Printf("    * %s\n", src)
			if outDir != "" && outDir != searchDir {
				os.MkdirAll(outDir, 0o755)
				dst := filepath.Join(outDir, e.Name())
				copyFile(src, dst)
				fmt.Printf("    -> %s\n", dst)
			}
		}
	}
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
	buf := make([]byte, 32*1024)
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr != nil {
			if rerr.Error() == "EOF" {
				break
			}
			return rerr
		}
	}
	return nil
}

func matchPattern(name, pattern string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return strings.Contains(name, pattern)
	}
	return re.MatchString(name)
}
