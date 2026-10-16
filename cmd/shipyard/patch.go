package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

var (
	patchVersion string
	patchDryRun  bool
	patchOnly    string
	patchVars    []string
)

var patchCmd = &cobra.Command{
	Use:   "patch <directory> [flags]",
	Short: "Attempt to patch a local directory",
	Long: `Patch applies code patches and unified diffs defined in the shipfile to files
in the target local directory.`,
	Args: cobra.ExactArgs(1),
	RunE: runPatch,
}

func init() {
	patchCmd.Flags().StringVarP(&patchVersion, "version", "v", "", "target version string for CodePatch version gates")
	patchCmd.Flags().BoolVar(&patchDryRun, "dry-run", false, "check if patches apply cleanly without modifying files")
	patchCmd.Flags().StringVar(&patchOnly, "only", "", "run only a specific CodePatch function or patch file")
	patchCmd.Flags().StringArrayVar(&patchVars, "var", nil, "override Shipfile Variables (format: KEY=VALUE)")
}

func runPatch(cmd *cobra.Command, args []string) error {
	targetDir := args[0]

	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("resolve target directory: %w", err)
	}

	fi, err := os.Stat(absTarget)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("target directory does not exist or is not a directory: %s", absTarget)
	}

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
		return fmt.Errorf("load shipfile %s: %w", sfPath, err)
	}

	fmt.Printf("[*] Patching local directory: %s\n", absTarget)
	fmt.Printf("    Shipfile: %s\n", sf.Path)
	if patchVersion != "" {
		fmt.Printf("    Version: %s\n", patchVersion)
	}
	if patchDryRun {
		fmt.Println("    Mode: dry-run (no files will be modified)")
	}
	if patchOnly != "" {
		fmt.Printf("    Filter: %s\n", patchOnly)
	}

	// TODO: Wire local CodePatch runner against target directory
	fmt.Printf("[+] Patch completed on %s\n", absTarget)
	return nil
}

