package incontainer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Run the setup step (install dependencies, prepare environment)",
		Long: `Setup runs the Shipfile's setup() method to prepare the build environment.
This step runs before source is available and is cached by Dagger independently
of the source tree.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error { return runGenericSetup() },
	}
}

func newBuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "Run the build step (compile only, patch+apply must have already run)",
		Long: `Build runs the compile step for the current SHIPYARD_MODE.

For package builds (SHIPYARD_MODE=package): invokes the distro packaging tool
(debuild, rpmbuild, abuild, makepkg) to compile from the already-patched source.

For generic builds (SHIPYARD_MODE=build or unset): loads the Shipfile and calls
its build() function directly.`,
		Args: cobra.NoArgs,
		RunE: runContainerBuild,
	}
}

func runContainerBuild(_ *cobra.Command, _ []string) error {
	mode := os.Getenv("SHIPYARD_MODE")
	switch mode {
	case "package":
		return runContainerPackageBuild()
	case "build", "":
		return runGenericBuild()
	default:
		return fmt.Errorf("unknown SHIPYARD_MODE %q (expected 'build' or 'package')", mode)
	}
}

func runGenericSetup() error {
	sfPath, err := getShipfilePath()
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
		return fmt.Errorf("load shipfile: %w", err)
	}

	version := os.Getenv("SHIPYARD_VERSION")

	if err := sf.Setup(shipfile.RunContext{Version: version}); err != nil {
		return err
	}
	fmt.Println("[+] Setup completed")
	return nil
}

func runGenericBuild() error {
	sfPath, err := getShipfilePath()
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
		return fmt.Errorf("load shipfile: %w", err)
	}

	version := os.Getenv("SHIPYARD_VERSION")
	sourceDir := os.Getenv("SHIPYARD_SOURCE")

	if sourceDir != "" {
		if err := os.Chdir(sourceDir); err != nil {
			return fmt.Errorf("chdir to SHIPYARD_SOURCE %s: %w", sourceDir, err)
		}
	}

	if err := sf.Build(shipfile.RunContext{
		SourceDir: sourceDir,
		Version:   version,
	}); err != nil {
		return err
	}
	fmt.Println("[+] Build completed")
	return nil
}

func runContainerPackageBuild() error {
	distro := os.Getenv("SHIPYARD_DISTRO")
	topDir := os.Getenv("SHIPYARD_TOPDIR")
	sourceDir := os.Getenv("SHIPYARD_SOURCE")
	pkgName := os.Getenv("SHIPYARD_PACKAGE")

	if sourceDir == "" {
		return fmt.Errorf("SHIPYARD_SOURCE is not set")
	}
	if distro == "" {
		return fmt.Errorf("SHIPYARD_DISTRO is not set")
	}
	if pkgName == "" {
		return fmt.Errorf("SHIPYARD_PACKAGE is not set")
	}

	fmt.Printf("[*] Building package (%s)...\n", distro)
	switch distro {
	case "debian":
		return runDebianBuild(topDir, sourceDir)
	case "rpm":
		return runRPMBuild(topDir, pkgName)
	case "alpine":
		return runAlpineBuild(topDir, pkgName)
	case "arch":
		return runArchBuild(topDir, sourceDir, pkgName)
	default:
		return fmt.Errorf("unsupported distro %q", distro)
	}
}

func runDebianBuild(topDir, sourceDir string) error {
	env := append(os.Environ(),
		"DEB_BUILD_OPTIONS=notest nocheck",
		"DEBUILD_DPKG_BUILDPACKAGE_OPTS=-d",
	)
	cmd := exec.Command("/bin/bash", "-c", "debuild --no-lintian -d -uc -us -b")
	cmd.Dir = sourceDir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("debuild: %w", err)
	}
	return nil
}

func runRPMBuild(topDir, pkgName string) error {
	rpmArgs := os.Getenv("SHIPYARD_RPM_ARGS")
	buildCmd := fmt.Sprintf("rpmbuild --define 'debug_package %%{nil}' --nocheck %s -bb SPECS/*.spec", rpmArgs)
	if err := shellIn(topDir, buildCmd); err != nil {
		return fmt.Errorf("rpmbuild: %w", err)
	}
	return nil
}

func runAlpineBuild(topDir, pkgName string) error {
	pkgDir := filepath.Join(topDir, pkgName)
	if err := shellIn(pkgDir, "abuild -F -r"); err != nil {
		return fmt.Errorf("abuild: %w", err)
	}
	return nil
}

func runArchBuild(topDir, sourceDir, pkgName string) error {
	pkgDir := filepath.Join(topDir, pkgName)
	if err := shellIn(pkgDir, "makepkg -e --noconfirm"); err != nil {
		return fmt.Errorf("makepkg: %w", err)
	}
	artifactsDir := filepath.Join(topDir, "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return fmt.Errorf("create artifacts dir: %w", err)
	}
	if err := shellIn(pkgDir, fmt.Sprintf("mv *.pkg.tar.zst %s/", artifactsDir)); err != nil {
		return fmt.Errorf("move artifacts: %w", err)
	}
	return nil
}
