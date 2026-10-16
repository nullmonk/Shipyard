package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

var (
	pullVersion string
	pullBranch  string
	pullForce   bool
)

var pullCmd = &cobra.Command{
	Use:   "pull <directory> [flags]",
	Short: "Pull the source to a local directory",
	Long: `Pull fetches upstream source code defined in the shipfile into the specified local directory.
Supports pulling from Git repositories (by tag, branch, or commit) or multi-URL tarball archives.`,
	Args: cobra.ExactArgs(1),
	RunE: runPull,
}

func init() {
	pullCmd.Flags().StringVarP(&pullVersion, "version", "v", "", "target version/tag to pull")
	pullCmd.Flags().StringVarP(&pullBranch, "branch", "b", "", "target branch to pull (defaults to Shipfile Branch)")
	pullCmd.Flags().BoolVar(&pullForce, "force", false, "overwrite destination directory if it already exists")
	pullCmd.Flags().BoolVar(&pullForce, "clean", false, "alias for --force")
}

func runPull(cmd *cobra.Command, args []string) error {
	destDir := args[0]

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

	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("resolve destination path: %w", err)
	}

	if fi, err := os.Stat(absDest); err == nil {
		if fi.IsDir() {
			entries, _ := os.ReadDir(absDest)
			if len(entries) > 0 && !pullForce {
				return fmt.Errorf("destination %s exists and is not empty (use --force to overwrite)", absDest)
			}
		} else if !pullForce {
			return fmt.Errorf("destination %s already exists and is not a directory (use --force to overwrite)", absDest)
		}
	}

	branch := pullBranch
	if branch == "" {
		branch = sf.Branch
	}

	version := pullVersion

	fmt.Printf("[*] Pulling source for %s to %s\n", sf.Name, absDest)
	if sf.Url != "" {
		fmt.Printf("    Source URL: %s\n", sf.Url)
	} else if len(sf.Urls) > 0 {
		fmt.Printf("    Source URLs: %d archive URLs\n", len(sf.Urls))
	}
	if version != "" {
		fmt.Printf("    Version: %s\n", version)
	}
	if branch != "" {
		fmt.Printf("    Branch: %s\n", branch)
	}

	// TODO: Wire to source manager / git clone / jumpstart implementation
	fmt.Printf("[+] Pull initialized for %s\n", absDest)
	return nil
}

