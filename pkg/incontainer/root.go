// Package incontainer defines the shipyard command tree used inside containers.
package incontainer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

var (
	globalShipfile string
	globalPkgDir   string
)

// Root returns the in-container cobra root command.
func Root() *cobra.Command {
	defaultSF := os.Getenv("SHIPFILE")
	if defaultSF == "" {
		defaultSF = "shipfile.py"
	}

	root := &cobra.Command{
		Use:          "shipyard",
		Short:        "In-container shipyard: patch, apply, build, reset",
		SilenceUsage: true,
		Long: `shipyard — in-container build helper

NORMAL BUILD PIPELINE (runs automatically):
  shipyard patch   Run CodePatch functions from the shipfile against SHIPYARD_SOURCE
  shipyard apply   Diff patched vs pristine, reset to clean, import patch into packaging system
  shipyard build   Compile the package (debuild / rpmbuild / abuild / makepkg)

INTERACTIVE DEBUGGING:
  Start with:   shipyard build --interactive   (from the HOST)
  This drops you into a shell after the build finishes or fails.

  Inside the shell you can:
    shipyard patch          Re-apply CodePatch modifications to the source
    shipyard apply          Re-generate diff, reset source, re-import patch
    shipyard build          Re-run the compile step only
    shipyard reset          Revert SHIPYARD_SOURCE to the pristine backup (cp -a)
    vim <file>              Edit files directly; vim is available in every container

  Typical debug loop:
    1. Edit a patch file or fix the shipfile:    vim $SHIPFILE
    2. Reset the source to clean:                shipyard reset
    3. Re-apply patches:                         shipyard patch
    4. Re-import into packaging system:          shipyard apply
    5. Re-compile:                               shipyard build

KEY ENVIRONMENT VARIABLES:
  SHIPYARD_SOURCE     Absolute path to the package source directory
  SHIPYARD_PRISTINE   cp -a backup of source before any patching (/tmp/.shipyard-pristine)
  SHIPYARD_TOPDIR     Top-level build directory (e.g. /tmp/build for debian/rpm)
  SHIPYARD_DISTRO     debian | rpm | alpine | arch
  SHIPYARD_PACKAGE    Package name (e.g. openssh)
  SHIPYARD_VERSION    Target version string
  SHIPFILE            Path to shipfile.py (default: shipfile.py)`,
	}

	root.PersistentFlags().StringVarP(&globalShipfile, "shipfile", "f", defaultSF, "path to shipfile.py ($SHIPFILE)")
	root.PersistentFlags().StringVar(&globalPkgDir, "shipyard-pkg", os.Getenv("SHIPYARD_PKG"), "directory containing the shipyard Python package ($SHIPYARD_PKG)")

	root.AddCommand(newPatchCmd())
	root.AddCommand(newApplyCmd())
	root.AddCommand(newBuildCmd())
	root.AddCommand(newResetCmd())

	return root
}

func getInterpreter() (*shipfile.Interpreter, error) {
	return shipfile.New(globalPkgDir)
}

func getShipfilePath() (string, error) {
	if globalShipfile == "" {
		return "", fmt.Errorf("no shipfile specified (use -f/--shipfile or $SHIPFILE)")
	}
	abs, err := filepath.Abs(globalShipfile)
	if err != nil {
		return "", fmt.Errorf("resolve shipfile path %s: %w", globalShipfile, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("shipfile not found: %s", abs)
	}
	if fi.IsDir() {
		candidate := filepath.Join(abs, "shipfile.py")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		return "", fmt.Errorf("directory %s does not contain a shipfile.py", abs)
	}
	return abs, nil
}

// shellIn runs a shell command in dir, streaming stdout/stderr.
func shellIn(dir, command string) error {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
