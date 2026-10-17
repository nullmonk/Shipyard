package incontainer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Reset source code to pristine state",
		Long: `Reset reverts all changes in SHIPYARD_SOURCE back to the pristine state.

For package builds: restores from the cp -a backup taken before patching.
For generic builds: restores from the git snapshot taken at build start.

Intended for use inside an interactive session started by 'shipyard build --interactive'.
Your shell environment (cwd, env vars, history) is not affected.`,
		Args: cobra.NoArgs,
		RunE: runReset,
	}
}

func runReset(_ *cobra.Command, _ []string) error {
	sourceDir := os.Getenv("SHIPYARD_SOURCE")
	if sourceDir == "" {
		return fmt.Errorf("SHIPYARD_SOURCE is not set")
	}

	if distro := os.Getenv("SHIPYARD_DISTRO"); distro != "" {
		return resetPackageSource(distro, sourceDir)
	}
	return resetGenericSource(sourceDir)
}

func resetPackageSource(distro, sourceDir string) error {
	pristine := os.Getenv("SHIPYARD_PRISTINE")
	if pristine == "" {
		return fmt.Errorf("SHIPYARD_PRISTINE is not set")
	}
	fmt.Printf("[*] Restoring %s source from pristine backup...\n", distro)
	if err := shellIn("/", fmt.Sprintf("rm -rf '%s' && cp -a '%s' '%s'", sourceDir, pristine, sourceDir)); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	fmt.Println("[+] Source reset to pristine state")
	return nil
}

func resetGenericSource(sourceDir string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is not installed; git is required to reset source")
	}
	if _, err := os.Stat(filepath.Join(sourceDir, ".git")); err != nil {
		return fmt.Errorf("no git snapshot in %s; start the session with --interactive to enable reset", sourceDir)
	}
	fmt.Printf("[*] Resetting %s to pristine state...\n", sourceDir)
	if err := shellIn(sourceDir, "git reset -q --hard HEAD && git clean -q -fdx"); err != nil {
		return fmt.Errorf("git reset: %w", err)
	}
	fmt.Printf("[+] %s reset to pristine state\n", sourceDir)
	return nil
}
