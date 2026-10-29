package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

var checkoutForce bool

func newCheckoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkout <version-or-tag> [directory]",
		Short: "Jump a local source tree to a version",
		Long: `Resolve a version (or tag) to an upstream git tag via version_to_tag
and check it out in a previously pulled source tree.

The directory defaults to $SHIPYARD_SOURCE, then the current directory.
Refuses to run with local changes unless --force is given.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runCheckout,
	}
	cmd.Flags().BoolVar(&checkoutForce, "force", false, "discard local changes when switching versions")
	return cmd
}

func runCheckout(cmd *cobra.Command, args []string) error {
	input := args[0]
	var dir string
	if len(args) > 1 {
		dir = args[1]
	} else if v := os.Getenv("SHIPYARD_SOURCE"); v != "" {
		dir = v
	} else {
		dir = "."
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve directory: %w", err)
	}
	if fi, err := os.Stat(filepath.Join(absDir, ".git")); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a git checkout (pull it first with `shipyard pull`)", absDir)
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

	if !checkoutForce {
		if out, err := runGit(absDir, "status", "--porcelain"); err == nil && strings.TrimSpace(out) != "" {
			return fmt.Errorf("working tree has local changes (use --force to discard them):\n%s", out)
		}
	}

	tag, err := resolveCheckoutTag(absDir, sf, input)
	if err != nil {
		return err
	}

	if _, err := runGit(absDir, "fetch", "--tags", "--quiet"); err != nil {
		fmt.Fprintf(os.Stderr, "[!] fetch --tags failed (offline?): %v\n", err)
	}
	if _, err := runGit(absDir, "show-ref", "--verify", "--quiet", "refs/tags/"+tag); err != nil {
		return fmt.Errorf("tag %q not present locally (fetch failed or unknown version %q)", tag, input)
	}

	checkoutArgs := []string{"checkout", tag}
	if checkoutForce {
		checkoutArgs = []string{"checkout", "-f", tag}
	}
	if out, err := runGit(absDir, checkoutArgs...); err != nil {
		return fmt.Errorf("checkout %s failed: %v\n%s", tag, err, out)
	}
	head, _ := runGit(absDir, "rev-parse", "--short", "HEAD")
	ver := input
	if v, err := sf.TagToVersion(tag); err == nil {
		ver = v
	}
	fmt.Printf("[+] %s now at %s (%s, %s)\n", absDir, tag, ver, strings.TrimSpace(head))
	return nil
}

// resolveCheckoutTag maps user input to a tag: an exact local tag wins,
// otherwise version_to_tag is tried, falling back to the raw input.
func resolveCheckoutTag(dir string, sf *shipfile.Shipfile, input string) (string, error) {
	if _, err := runGit(dir, "show-ref", "--verify", "--quiet", "refs/tags/"+input); err == nil {
		return input, nil
	}
	return resolveTag(sf, input), nil
}
