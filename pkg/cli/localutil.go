package cli

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"shipyard/pkg/shipfile"
)

// This file holds small, dependency-free helpers for the local source
// workflow commands (versions, checkout, pull, patch --dry-run, export,
// check). They are deliberately free of Dagger/network side effects so they
// stay unit-testable.

// runGit runs git with the given args in dir and returns combined output.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// isArchiveURL reports whether a URL points at a source archive rather than
// a git repository.
func isArchiveURL(rawurl string) bool {
	lower := strings.ToLower(strings.Split(rawurl, "?")[0])
	for _, ext := range []string{".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".zip", ".tar"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// matchVersionTags reports whether tag matches a VersionTags glob pattern
// (e.g. "V_*", "release-*", "V_*_P[0123456789]").
func matchVersionTags(pattern, tag string) bool {
	if pattern == "" {
		pattern = "*"
	}
	ok, err := path.Match(pattern, tag)
	if err != nil {
		return tag == pattern
	}
	return ok
}

// parseLsRemoteTags extracts unique tag names from `git ls-remote --tags`
// output, dropping ^{} dereference duplicates in favor of the tag itself.
func parseLsRemoteTags(out string) []string {
	var tags []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		ref := fields[1]
		if !strings.HasPrefix(ref, "refs/tags/") {
			continue
		}
		tag := strings.TrimPrefix(ref, "refs/tags/")
		deref := false
		if strings.HasSuffix(tag, "^{}") {
			tag = strings.TrimSuffix(tag, "^{}")
			deref = true
		}
		if tag == "" || seen[tag] {
			continue
		}
		// Prefer the non-dereferenced entry: if we already recorded via a
		// plain entry, skip; a deref entry only records when unseen.
		seen[tag] = true
		_ = deref
		tags = append(tags, tag)
	}
	return tags
}

// splitVersionParts splits a version string into its numeric components,
// mirroring the _vparts helpers used in shipfiles.
func splitVersionParts(s string) []int {
	var parts []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return r < '0' || r > '9'
	}) {
		if f == "" {
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		parts = append(parts, n)
	}
	return parts
}

// compareVersions orders version strings by numeric components, with a
// lexical tiebreak so the order is total and deterministic.
func compareVersions(a, b string) int {
	pa, pb := splitVersionParts(a), splitVersionParts(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(pa) {
			ai = pa[i]
		}
		if i < len(pb) {
			bi = pb[i]
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return strings.Compare(a, b)
}

// versionFromArchiveName derives a version tag from a source archive file
// name (e.g. "vsftpd-3.0.5.tar.gz" -> "3.0.5"). It strips known archive
// extensions, then takes the suffix after the last '-' or '_' when that
// suffix starts with a digit; otherwise it returns the stripped name.
func versionFromArchiveName(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tgz", ".tbz2", ".txz", ".zip", ".tar"} {
		if strings.HasSuffix(lower, ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}
	for _, sep := range []string{"-", "_"} {
		if i := strings.LastIndex(name, sep); i >= 0 && i+1 < len(name) {
			if rest := name[i+1:]; rest[0] >= '0' && rest[0] <= '9' {
				return rest
			}
		}
	}
	return name
}

// resolveTag maps a user-supplied version (or tag) to an upstream git tag
// using the Shipfile's version_to_tag hook, falling back to the raw input
// when the hook rejects it (i.e. the input already looks like a tag).
func resolveTag(sf *shipfile.Shipfile, input string) string {
	if tag, err := sf.VersionToTag(input); err == nil && tag != "" {
		return tag
	}
	return input
}

// copyTree replicates src into dst, creating dst if needed. Top-level
// entries named in exclude (e.g. ".git") are skipped. Symlinks are recreated,
// not followed.
func copyTree(src, dst string, exclude map[string]bool) error {
	return filepath.Walk(src, func(full string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if top := strings.Split(filepath.ToSlash(rel), "/")[0]; exclude[top] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(full)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(full)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

// treeEntry is a snapshot of one path in a source tree.
type treeEntry struct {
	isDir  bool
	hash   [32]byte // content hash for files/symlinks; zero for dirs
	hasSum bool
}

// snapshotTree records every path (slash-separated, relative) under root,
// skipping excluded top-level entries.
func snapshotTree(root string, exclude map[string]bool) (map[string]treeEntry, error) {
	out := map[string]treeEntry{}
	err := filepath.Walk(root, func(full string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if top := strings.SplitN(rel, "/", 2)[0]; exclude[top] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			out[rel] = treeEntry{isDir: true}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(full)
			if err != nil {
				return err
			}
			out[rel] = treeEntry{hash: sha256.Sum256([]byte(link)), hasSum: true}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		out[rel] = treeEntry{hash: sum, hasSum: true}
		return nil
	})
	return out, err
}

// treeDiff describes one path that differs between two trees.
type treeDiff struct {
	Path string // slash-separated, relative
	Kind string // "added", "removed", or "modified"
}

// compareTrees diffs two directory trees (excluding top-level exclude names),
// returning sorted per-path differences. Directory-only additions are
// reported for empty dirs; otherwise file changes imply their parents.
func compareTrees(a, b string, exclude map[string]bool) ([]treeDiff, error) {
	ma, err := snapshotTree(a, exclude)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", a, err)
	}
	mb, err := snapshotTree(b, exclude)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", b, err)
	}
	var diffs []treeDiff
	for p, ea := range ma {
		eb, ok := mb[p]
		if !ok {
			if !ea.isDir {
				diffs = append(diffs, treeDiff{Path: p, Kind: "removed"})
			}
			continue
		}
		if ea.isDir != eb.isDir {
			diffs = append(diffs, treeDiff{Path: p, Kind: "modified"})
			continue
		}
		if !ea.isDir && ea.hash != eb.hash {
			diffs = append(diffs, treeDiff{Path: p, Kind: "modified"})
		}
	}
	for p, eb := range mb {
		if _, ok := ma[p]; !ok && !eb.isDir {
			diffs = append(diffs, treeDiff{Path: p, Kind: "added"})
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Path < diffs[j].Path })
	return diffs, nil
}

// balanceIssue is a single delimiter-balance problem in a source file.
type balanceIssue struct {
	Line int
	Msg  string
}

// checkBalance verifies that (), [], and {} delimiters balance in the file
// at path, ignoring C comments, string/char literals (with escapes). It
// returns every mismatch found (empty means balanced).
func checkBalance(path string) ([]balanceIssue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	type opener struct {
		ch   byte
		line int
	}
	var stack []opener
	var issues []balanceIssue
	line := 1
	i := 0
	for i < len(data) {
		c := data[i]
		// Line comment.
		if c == '/' && i+1 < len(data) && data[i+1] == '/' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		// Block comment.
		if c == '/' && i+1 < len(data) && data[i+1] == '*' {
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				if data[i] == '\n' {
					line++
				}
				i++
			}
			i += 2
			continue
		}
		// String or char literal.
		if c == '"' || c == '\'' {
			quote := c
			i++
			for i < len(data) {
				if data[i] == '\\' {
					i += 2
					continue
				}
				if data[i] == '\n' && quote == '"' {
					break // unterminated string; let the compiler complain
				}
				if data[i] == quote {
					i++
					break
				}
				if data[i] == '\n' {
					line++
				}
				i++
			}
			continue
		}
		switch c {
		case '\n':
			line++
		case '(', '[', '{':
			stack = append(stack, opener{ch: c, line: line})
		case ')', ']', '}':
			var want byte
			switch c {
			case ')':
				want = '('
			case ']':
				want = '['
			default:
				want = '{'
			}
			if len(stack) == 0 {
				issues = append(issues, balanceIssue{Line: line, Msg: fmt.Sprintf("unmatched closing %q", c)})
			} else {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if top.ch != want {
					issues = append(issues, balanceIssue{Line: line, Msg: fmt.Sprintf("closing %q does not match %q opened at line %d", c, top.ch, top.line)})
				}
			}
		}
		i++
	}
	for _, o := range stack {
		issues = append(issues, balanceIssue{Line: o.line, Msg: fmt.Sprintf("unclosed %q", o.ch)})
	}
	return issues, nil
}

// loadAndValidateShipfile loads the shipfile at sfPath with interp and
// validates it, returning the parsed Shipfile.
func loadAndValidateShipfile(interp *shipfile.Interpreter, sfPath string) (*shipfile.Shipfile, error) {
	sf, err := shipfile.Load(sfPath, interp)
	if err != nil {
		return nil, fmt.Errorf("load shipfile %s: %w", sfPath, err)
	}
	if err := sf.Validate(); err != nil {
		return nil, fmt.Errorf("shipfile %s is invalid: %w", sfPath, err)
	}
	return sf, nil
}
