package cli

import (
	"bytes"
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

	sf, err := loadAndValidateShipfile(interp, sfPath)
	if err != nil {
		return err
	}

	fmt.Printf("[*] Patching: %s\n", absTarget)
	if version != "" {
		fmt.Printf("    Version: %s\n", version)
	}
	if patchDryRun {
		return dryRunPatches(sf, absTarget, version)
	}

	applied, err := sf.RunCodePatches(absTarget, version, os.Stdout)
	if err != nil {
		return fmt.Errorf("run code patches: %w", err)
	}
	fmt.Printf("[+] Patch completed: %d function(s) applied\n", len(applied))
	return nil
}

// dryRunPatches applies the shipfile's CodePatches to a scratch copy of the
// target tree and reports what would change, leaving the original untouched.
func dryRunPatches(sf *shipfile.Shipfile, absTarget, version string) error {
	fmt.Printf("[*] Dry-run patching (copy of %s)\n", absTarget)
	if version != "" {
		fmt.Printf("    Version: %s\n", version)
	}
	tmp, err := os.MkdirTemp("", "shipyard-dryrun-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(absTarget, tmp, map[string]bool{".git": true}); err != nil {
		return fmt.Errorf("copy source for dry-run: %w", err)
	}
	var buf bytes.Buffer
	applied, err := sf.RunCodePatches(tmp, version, &buf)
	if err != nil {
		return fmt.Errorf("dry-run: patches do NOT apply cleanly: %w", err)
	}
	fmt.Print(buf.String())
	diffs, err := compareTrees(absTarget, tmp, map[string]bool{".git": true})
	if err != nil {
		return fmt.Errorf("dry-run compare: %w", err)
	}
	if len(diffs) == 0 {
		fmt.Println("[=] Dry-run: patches ran but changed no files")
		return nil
	}
	fmt.Printf("[+] Dry-run: %d function(s) applied, %d file(s) would change:\n", len(applied), len(diffs))
	for _, d := range diffs {
		fmt.Printf("    %s %s\n", map[string]string{"added": "A", "removed": "D", "modified": "M"}[d.Kind], d.Path)
	}
	return nil
}
