package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipyard"
)

var (
	globalShipfile string
	globalRemote   string
	globalPkgDir   string
)

var rootCmd = &cobra.Command{
	Use:   "shipyard",
	Short: "Shipyard executes and builds shipfile projects using Dagger and gpython",
	Long: `Shipyard is a containerized build and patching tool that builds generic software
and OS packages using Dagger, local gpython interpreters, and declarative Shipfiles.`,
	SilenceUsage: true,
}

func init() {
	defaultSF := os.Getenv("SHIPFILE")
	if defaultSF == "" {
		defaultSF = "shipfile.py"
	}

	defaultRemote := os.Getenv("SHIPYARD_REMOTE")

	defaultPkg := os.Getenv("SHIPYARD_PKG")
	if defaultPkg == "" {
		defaultPkg = defaultShipyardPkg()
	}

	rootCmd.PersistentFlags().StringVarP(&globalShipfile, "shipfile", "f", defaultSF, "path to shipfile.py ($SHIPFILE)")
	rootCmd.PersistentFlags().StringVar(&globalRemote, "remote", defaultRemote, "remote Shipyard HTTP server URL ($SHIPYARD_REMOTE)")
	rootCmd.PersistentFlags().StringVar(&globalPkgDir, "shipyard-pkg", defaultPkg, "directory containing the shipyard Python package ($SHIPYARD_PKG)")

	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(pullCmd)
	rootCmd.AddCommand(patchCmd)
	rootCmd.AddCommand(remoteCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(validateCmd)
}

func getInterpreter() (*shipyard.Interpreter, error) {
	return shipyard.New(globalPkgDir)
}

func resolveShipfilePath(target string) (string, error) {
	if target != "" {
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
	return getShipfilePath()
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

func defaultShipyardPkg() string {
	return os.Getenv("SHIPYARD_PKG")
}

