package cli

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
)

func newPatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "patch [directory]",
		Short: "Apply code patches to a source directory",
		Long: `Patch applies @CodePatch functions, pre_patch, and post_patch hooks defined in
the shipfile to files in the target directory.

The directory argument is optional when SHIPYARD_SOURCE is set.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runPatch,
	}
	cmd.Flags().StringVarP(&patchVersion, "version", "v", "", "target version string for CodePatch version gates ($SHIPYARD_VERSION)")
	cmd.Flags().BoolVar(&patchDryRun, "dry-run", false, "check if patches apply cleanly without modifying files")
	return cmd
}

func runPatch(cmd *cobra.Command, args []string) error {
	srcDir := ""
	if len(args) > 0 {
		srcDir = args[0]
	} else if v := os.Getenv("SHIPYARD_SOURCE"); v != "" {
		srcDir = v
	} else {
		return fmt.Errorf("source directory required: pass as argument or set SHIPYARD_SOURCE")
	}

	absTarget, err := filepath.Abs(srcDir)
	if err != nil {
		return fmt.Errorf("resolve target directory: %w", err)
	}
	fi, err := os.Stat(absTarget)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("target directory does not exist or is not a directory: %s", absTarget)
	}

	version := patchVersion
	if version == "" {
		version = os.Getenv("SHIPYARD_VERSION")
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
	if err := sf.Validate(); err != nil {
		return fmt.Errorf("shipfile %s is invalid: %w", sfPath, err)
	}

	fmt.Printf("[*] Patching: %s\n", absTarget)
	if version != "" {
		fmt.Printf("    Version: %s\n", version)
	}
	if patchDryRun {
		fmt.Println("    Mode: dry-run (no files will be modified)")
		return nil
	}

	applied, err := sf.RunCodePatches(absTarget, version, os.Stdout)
	if err != nil {
		return fmt.Errorf("run code patches: %w", err)
	}
	fmt.Printf("[+] Patch completed: %d function(s) applied\n", len(applied))
	return nil
}
