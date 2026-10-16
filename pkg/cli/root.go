// Package cli defines the host-side shipyard command tree.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

var (
	globalShipfile string
	globalRemote   string
	globalPkgDir   string
)

// Root returns the host-side cobra root command.
func Root() *cobra.Command {
	defaultSF := os.Getenv("SHIPFILE")
	if defaultSF == "" {
		defaultSF = "shipfile.py"
	}

	root := &cobra.Command{
		Use:          "shipyard",
		Short:        "Build and patch shipfile projects using Dagger",
		Long:         `Shipyard is a containerized build and patching tool that builds generic software and OS packages using Dagger, gpython, and declarative Shipfiles.`,
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(&globalShipfile, "shipfile", "f", defaultSF, "path to shipfile.py ($SHIPFILE)")
	root.PersistentFlags().StringVar(&globalRemote, "remote", os.Getenv("SHIPYARD_REMOTE"), "remote Shipyard HTTP server URL ($SHIPYARD_REMOTE)")
	root.PersistentFlags().StringVar(&globalPkgDir, "shipyard-pkg", os.Getenv("SHIPYARD_PKG"), "directory containing the shipyard Python package ($SHIPYARD_PKG)")

	root.AddCommand(newBuildCmd())
	root.AddCommand(newPullCmd())
	root.AddCommand(newPatchCmd())
	root.AddCommand(newInfoCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newRemoteCmd())

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

func resolveShipfilePath(target string) (string, error) {
	if target == "" {
		return getShipfilePath()
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve shipfile path %s: %w", target, err)
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
