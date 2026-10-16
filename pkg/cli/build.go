package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"shipyard/pkg/builder"
	"shipyard/pkg/shipfile"
)

var (
	buildPackageImage string
	buildPackageName  string
	buildImage        string
	buildVersion      string
	buildOutput       string
	buildArtifacts    string
	buildInteractive  bool
	buildLocal        bool
)

func newBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [flags] [shipfile]",
		Short: "Build a generic project or distro package",
		Long: `Build executes a shipfile.

Without --package: runs a generic build in a container (BaseImage).
With --package <image>: spins up a distro container to create an OS package.
With --local: runs setup() and build() directly on the host without Dagger.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runBuild,
	}
	cmd.Flags().BoolVarP(&buildLocal, "local", "l", false, "run setup() and build() directly on host without Dagger")
	cmd.Flags().StringVarP(&buildPackageImage, "package", "p", "", "distro container image for package build (e.g. ubuntu:22.04)")
	cmd.Flags().StringVar(&buildPackageName, "package-name", "", "override package name (defaults to Shipfile PackageName or Name)")
	cmd.Flags().StringVarP(&buildImage, "image", "i", "", "base container image for generic build (defaults to Shipfile BaseImage)")
	cmd.Flags().StringVarP(&buildVersion, "version", "v", "", "target version/tag to build ($SHIPYARD_VERSION)")
	cmd.Flags().StringVarP(&buildOutput, "output", "o", "", "output directory for artifacts (default: build-output)")
	cmd.Flags().StringVarP(&buildArtifacts, "artifacts", "a", "", "artifact regex pattern")
	cmd.Flags().BoolVar(&buildInteractive, "interactive", false, "drop into interactive container shell on finish/error")
	return cmd
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

	sf, err := shipfile.Load(sfPath, interp)
	if err != nil {
		return fmt.Errorf("load shipfile %s: %w", sfPath, err)
	}

	if err := sf.Validate(); err != nil {
		return fmt.Errorf("validate shipfile %s: %w", sfPath, err)
	}

	version := buildVersion
	if version == "" {
		version = os.Getenv("SHIPYARD_VERSION")
	}

	if buildLocal {
		return runLocalBuild(sf, interp, version, buildOutput, buildArtifacts)
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
		res, err := builder.BuildPackage(ctx, engine.Client(), sf, buildPackageImage, builder.BuildPackageOpts{
			PackageName:     buildPackageName,
			Version:         version,
			ArtifactPattern: buildArtifacts,
			OutputDir:       outDir,
			Interactive:     buildInteractive,
		})
		if err != nil {
			return fmt.Errorf("package build failed: %w", err)
		}
		fmt.Printf("[+] Package build completed: %d artifacts in %s\n", len(res.Artifacts), res.OutputDir)
		return nil
	}

	outDir := buildOutput
	if outDir == "" {
		outDir = "build-output"
	}
	res, err := builder.BuildShipfile(ctx, engine.Client(), sf, builder.BuildShipfileOpts{
		Image:           buildImage,
		Version:         version,
		ArtifactPattern: buildArtifacts,
		OutputDir:       outDir,
		Interactive:     buildInteractive,
	})
	if err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Printf("[+] Build completed: %d artifacts in %s\n", len(res.Artifacts), res.OutputDir)
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
