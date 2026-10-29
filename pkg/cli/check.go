package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	checkStrict  bool
	checkCompile bool
)

func newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check [directory]",
		Short: "Syntax-check a patched source tree",
		Long: `Run lightweight, container-free checks on a (patched) source tree:

  1. ` + "`git diff --check`" + ` for whitespace errors (git checkouts only),
  2. delimiter-balance scan ((), [], {}) of changed C-like files,
  3. with --compile, ` + "`gcc -fsyntax-only`" + ` on changed C files.

This is a fast local gate before a full package build; it does not replace
running the project's own build. Independent of Dagger and remotes.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runCheck,
	}
	cmd.Flags().BoolVar(&checkStrict, "strict", false, "fail on any advisory finding, not just errors")
	cmd.Flags().BoolVar(&checkCompile, "compile", false, "attempt gcc -fsyntax-only on changed C files (requires gcc and headers)")
	return cmd
}

func runCheck(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	} else if v := os.Getenv("SHIPYARD_SOURCE"); v != "" {
		dir = v
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve directory: %w", err)
	}
	if fi, err := os.Stat(absDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("directory does not exist or is not a directory: %s", absDir)
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
	if _, err := loadAndValidateShipfile(interp, sfPath); err != nil {
		return err
	}

	isGit := false
	if _, err := os.Stat(filepath.Join(absDir, ".git")); err == nil {
		isGit = true
	}

	var candidates []string
	if isGit {
		changed, err := gitChangedFiles(absDir)
		if err != nil {
			return err
		}
		candidates = changed
	} else {
		fmt.Println("[*] Not a git checkout: scanning all C-like files")
		candidates, err = allCLikeFiles(absDir)
		if err != nil {
			return err
		}
	}

	failures := 0
	warnings := 0

	// 1. Whitespace errors.
	if isGit {
		if out, err := runGit(absDir, "diff", "--check"); err != nil {
			// git diff --check exits non-zero when problems exist.
			fmt.Printf("[!] git diff --check found whitespace errors:\n%s\n", out)
			failures++
		} else {
			fmt.Println("[+] git diff --check: clean")
		}
	}

	// 2. Balance scan of C-like files.
	cfiles := filterCLike(candidates, isGit, absDir)
	if len(cfiles) == 0 && isGit {
		fmt.Println("[*] No changed C-like files to scan")
	} else {
		for _, f := range cfiles {
			display := f
			if p, err := filepath.Rel(absDir, f); err == nil {
				display = p
			}
			issues, err := checkBalance(f)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[!] cannot read %s: %v\n", display, err)
				failures++
				continue
			}
			for _, is := range issues {
				fmt.Printf("[!] %s:%d: %s\n", display, is.Line, is.Msg)
				failures++
			}
		}
		if failures == 0 {
			fmt.Printf("[+] delimiter balance: clean (%d C-like file(s) scanned)\n", len(cfiles))
		}
	}

	// 3. Optional gcc syntax check.
	cSrcs := filterCSources(cfiles)
	if checkCompile {
		gcc, err := exec.LookPath("gcc")
		if err != nil {
			return fmt.Errorf("--compile requires gcc, which was not found in PATH")
		}
		if len(cSrcs) == 0 {
			fmt.Println("[*] --compile: no changed C sources to check")
		}
		for _, f := range cSrcs {
			display := f
			if p, err := filepath.Rel(absDir, f); err == nil {
				display = p
			}
			cc := exec.Command(gcc, "-fsyntax-only", "-I"+absDir, f)
			cc.Dir = absDir
			if out, err := cc.CombinedOutput(); err != nil {
				fmt.Printf("[!] gcc -fsyntax-only %s failed:\n%s\n", display, out)
				failures++
			} else if len(strings.TrimSpace(string(out))) > 0 {
				fmt.Printf("[!] gcc -fsyntax-only %s warnings:\n%s\n", display, out)
				warnings++
			} else {
				fmt.Printf("[+] gcc -fsyntax-only %s: clean\n", display)
			}
		}
	} else if len(cSrcs) > 0 {
		fmt.Println("[*] Tip: --compile attempts gcc -fsyntax-only on changed C files (needs project headers/configure output)")
	}

	if failures > 0 {
		return fmt.Errorf("check failed: %d problem(s)", failures)
	}
	if warnings > 0 && checkStrict {
		return fmt.Errorf("check failed in --strict mode: %d warning(s)", warnings)
	}
	fmt.Println("[+] check passed")
	return nil
}

// gitChangedFiles lists added/modified/untracked (not deleted) paths in a
// git working tree, repo-rooted as absolute paths.
func gitChangedFiles(dir string) ([]string, error) {
	out, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	top, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	top = strings.TrimSpace(top)
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		status, rest := line[:2], strings.TrimSpace(line[3:])
		if strings.Contains(status, "D") {
			continue // deleted: nothing to check
		}
		rel := rest
		if i := strings.LastIndex(rest, " -> "); i >= 0 {
			rel = rest[i+4:] // renames: check the new path
		}
		rel = strings.Trim(rel, "\"")
		if rel == "" {
			continue
		}
		files = append(files, filepath.Join(top, filepath.FromSlash(rel)))
	}
	return files, nil
}

// allCLikeFiles walks dir for C-like sources (used outside git checkouts).
func allCLikeFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.Walk(dir, func(full string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if isCLike(filepath.ToSlash(full)) {
			out = append(out, full)
		}
		return nil
	})
	return out, err
}

// filterCLike keeps C-like files; for git checkouts the input is already the
// changed set, otherwise it globs the tree.
func filterCLike(files []string, isGit bool, absDir string) []string {
	if !isGit {
		return files // already C-like via allCLikeFiles
	}
	var out []string
	for _, f := range files {
		if isCLike(f) {
			out = append(out, f)
		}
	}
	return out
}

func isCLike(p string) bool {
	lower := strings.ToLower(p)
	for _, ext := range []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hh", ".hpp"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// filterCSources keeps compilable C sources (headers are included by
// sources, not compiled directly).
func filterCSources(files []string) []string {
	var out []string
	for _, f := range files {
		lower := strings.ToLower(f)
		if strings.HasSuffix(lower, ".c") || strings.HasSuffix(lower, ".cc") ||
			strings.HasSuffix(lower, ".cpp") || strings.HasSuffix(lower, ".cxx") {
			out = append(out, f)
		}
	}
	return out
}
