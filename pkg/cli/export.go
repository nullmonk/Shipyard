package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	exportVersion string
	exportOutput  string
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export [directory] [flags]",
		Short: "Export local patches as a unified diff",
		Long: `Apply the shipfile's CodePatches to a scratch copy of the source
tree and print the resulting unified diff (orig vs patched), leaving the
original tree untouched.

The diff can be fed to distro packaging (quilt, debian/patches) or kept as
a plain patchfile, independent of containerized package builds.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runExport,
	}
	cmd.Flags().StringVarP(&exportVersion, "version", "v", "", "target version string for CodePatch version gates ($SHIPYARD_VERSION)")
	cmd.Flags().StringVarP(&exportOutput, "output", "o", "", "write diff to file instead of stdout")
	return cmd
}

func runExport(cmd *cobra.Command, args []string) error {
	srcDir := ""
	if len(args) > 0 {
		srcDir = args[0]
	} else if v := os.Getenv("SHIPYARD_SOURCE"); v != "" {
		srcDir = v
	} else {
		return fmt.Errorf("source directory required: pass as argument or set SHIPYARD_SOURCE")
	}

	absSrc, err := filepath.Abs(srcDir)
	if err != nil {
		return fmt.Errorf("resolve source directory: %w", err)
	}
	if fi, err := os.Stat(absSrc); err != nil || !fi.IsDir() {
		return fmt.Errorf("source directory does not exist or is not a directory: %s", absSrc)
	}

	version := exportVersion
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

	tmp, err := os.MkdirTemp("", "shipyard-export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(absSrc, tmp, map[string]bool{".git": true}); err != nil {
		return fmt.Errorf("copy source for export: %w", err)
	}

	if _, err := sf.RunCodePatches(tmp, version, devNullWriter{}); err != nil {
		return fmt.Errorf("run code patches: %w", err)
	}

	diffs, err := compareTrees(absSrc, tmp, map[string]bool{".git": true})
	if err != nil {
		return fmt.Errorf("compare patched tree: %w", err)
	}
	if len(diffs) == 0 {
		fmt.Fprintf(os.Stderr, "[=] Export: patches applied but changed no files\n")
		return nil
	}

	if _, err := exec.LookPath("diff"); err != nil {
		return fmt.Errorf("export needs the `diff` binary: %w", err)
	}
	diffCmd := exec.Command("diff", "-ruN", "--exclude=.git", absSrc, tmp)
	diffCmd.Dir = filepath.Dir(absSrc)
	out, err := diffCmd.Output()
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		// Exit 1 means differences were found: the normal case.
		err = nil
	}
	if err != nil {
		return fmt.Errorf("diff patched tree: %w", err)
	}

	if exportOutput != "" {
		if err := os.WriteFile(exportOutput, out, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", exportOutput, err)
		}
		fmt.Fprintf(os.Stderr, "[+] Export: %d file(s) changed, diff written to %s\n", len(diffs), exportOutput)
		return nil
	}
	fmt.Printf("%s", out)
	return nil
}

// devNullWriter discards RunCodePatches progress output during export.
type devNullWriter struct{}

func (devNullWriter) Write(p []byte) (int, error) { return len(p), nil }
