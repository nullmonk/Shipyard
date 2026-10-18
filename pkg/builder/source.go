package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
)

// SourceConfig defines where and how source code is fetched.
type SourceConfig struct {
	// Url is the Git repository URL (e.g. "https://github.com/openssh/openssh-portable").
	Url string
	URL string // backwards compatibility

	// Branch specifies a Git branch to checkout.
	Branch string

	// Tag specifies a Git tag to checkout.
	Tag string

	// Commit specifies a Git commit SHA to checkout.
	Commit string

	// LocalDir specifies a directory on the host to load source code from directly.
	LocalDir string

	// Exclude lists file/directory patterns to exclude when loading from LocalDir.
	Exclude []string

	// Include lists file/directory patterns to include when loading from LocalDir.
	Include []string
}

// IgnoreRules stores parsed patterns from .shipignore or .dockerignore.
type IgnoreRules struct {
	Excludes    []string
	Includes    []string
	HasWildcard bool
	RawRules    []string
}

// ReadIgnoreRules reads and parses .shipignore, then .dockerignore, from dir.
func ReadIgnoreRules(dir string) IgnoreRules {
	for _, name := range []string{".shipignore", ".dockerignore"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var excludes, includes, raw []string
		hasWildcard := false
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			raw = append(raw, line)
			if strings.HasPrefix(line, "!") {
				inc := strings.TrimPrefix(line, "!")
				includes = append(includes, inc)
			} else {
				if line == "*" {
					hasWildcard = true
				}
				excludes = append(excludes, line)
			}
		}
		return IgnoreRules{
			Excludes:    excludes,
			Includes:    includes,
			HasWildcard: hasWildcard,
			RawRules:    raw,
		}
	}
	return IgnoreRules{}
}

// readIgnorePatterns reads .shipignore, then .dockerignore, from dir.
func readIgnorePatterns(dir string) []string {
	rules := ReadIgnoreRules(dir)
	if rules.HasWildcard && len(rules.Includes) > 0 {
		var out []string
		for _, ex := range rules.Excludes {
			if ex != "*" {
				out = append(out, ex)
			}
		}
		return out
	}
	return rules.Excludes
}

func matchSinglePattern(rel, pat string) bool {
	pat = strings.TrimSuffix(pat, "/")
	if pat == "*" {
		return true
	}
	base := filepath.Base(rel)
	if rel == pat || base == pat {
		return true
	}
	if matched, _ := filepath.Match(pat, rel); matched {
		return true
	}
	if matched, _ := filepath.Match(pat, base); matched {
		return true
	}
	if strings.HasPrefix(rel, pat+"/") {
		return true
	}
	if after, ok := strings.CutSuffix(pat, "/*"); ok {
		if rel == after || strings.HasPrefix(rel, after+"/") {
			return true
		}
	}
	if after, ok := strings.CutPrefix(pat, "**/"); ok {
		if matched, _ := filepath.Match(after, base); matched {
			return true
		}
		if rel == after || strings.HasPrefix(rel, after+"/") {
			return true
		}
	}
	return false
}

func isAncestorOfInclude(rel string, includes []string) bool {
	cleanRel := strings.TrimSuffix(filepath.ToSlash(rel), "/")
	for _, inc := range includes {
		inc = strings.TrimSuffix(filepath.ToSlash(inc), "/")
		if inc == cleanRel || strings.HasPrefix(inc, cleanRel+"/") {
			return true
		}
		parts := strings.Split(inc, "/")
		accum := ""
		for _, p := range parts {
			if accum == "" {
				accum = p
			} else {
				accum += "/" + p
			}
			if accum == cleanRel {
				return true
			}
		}
	}
	return false
}

// matchesIgnorePattern reports whether rel (a slash-separated path relative to
// the archive root) matches any of the ignore patterns.
func matchesIgnorePattern(rel string, patterns []string) bool {
	for _, pat := range patterns {
		if matchSinglePattern(rel, pat) {
			return true
		}
	}
	return false
}

func shouldIgnorePath(rel string, isDir bool, rules IgnoreRules, extra []string) bool {
	if matchesIgnorePattern(rel, defaultExcludes) {
		return true
	}
	if matchesIgnorePattern(rel, extra) {
		return true
	}
	if isDir && isAncestorOfInclude(rel, rules.Includes) {
		return false
	}
	if rules.HasWildcard && len(rules.Includes) > 0 {
		for _, inc := range rules.Includes {
			if matchSinglePattern(rel, inc) {
				return false
			}
		}
		return true
	}
	ignored := false
	for _, rule := range rules.RawRules {
		if strings.HasPrefix(rule, "!") {
			if matchSinglePattern(rel, strings.TrimPrefix(rule, "!")) {
				ignored = false
			}
		} else {
			if matchSinglePattern(rel, rule) {
				ignored = true
			}
		}
	}
	return ignored
}

// defaultExcludes are always omitted from local-directory copies.
var defaultExcludes = []string{".git", "__pycache__", "build-output", "sources"}

// ArchiveDir creates a gzip-compressed tar of dir, honouring .shipignore /
// .dockerignore and the caller-supplied extra patterns. Returns the raw bytes.
func ArchiveDir(dir string, extra []string) ([]byte, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve archive dir %s: %w", dir, err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("archive source directory does not exist: %s", abs)
	}

	rules := ReadIgnoreRules(abs)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err = filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if shouldIgnorePath(rel, info.IsDir(), rules, extra) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PullSource fetches source code into a Dagger directory either from a Git repository
// or from a local host directory.
func PullSource(ctx context.Context, client *dagger.Client, cfg SourceConfig) (*dagger.Directory, error) {
	if cfg.LocalDir != "" {
		abs, err := filepath.Abs(cfg.LocalDir)
		if err != nil {
			return nil, fmt.Errorf("resolve local source path %s: %w", cfg.LocalDir, err)
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("local source directory does not exist: %s", abs)
		}

		exclude := cfg.Exclude
		include := cfg.Include
		if len(exclude) == 0 && len(include) == 0 {
			rules := ReadIgnoreRules(abs)
			if rules.HasWildcard && len(rules.Includes) > 0 {
				var nonWildcardExcludes []string
				for _, ex := range rules.Excludes {
					if ex != "*" {
						nonWildcardExcludes = append(nonWildcardExcludes, ex)
					}
				}
				exclude = append([]string{".git/", "__pycache__/", "build-output/", "sources/"}, nonWildcardExcludes...)
				include = rules.Includes
			} else {
				exclude = append([]string{".git/", "__pycache__/", "build-output/", "sources/"}, rules.Excludes...)
				include = rules.Includes
			}
		}

		opts := dagger.HostDirectoryOpts{
			Exclude: exclude,
		}
		if len(include) > 0 {
			opts.Include = include
		}

		return client.Host().Directory(abs, opts), nil
	}

	gitUrl := cfg.Url
	if gitUrl == "" {
		gitUrl = cfg.URL
	}
	if gitUrl == "" {
		return nil, fmt.Errorf("either Url or LocalDir must be provided to pull source")
	}

	repo := client.Git(gitUrl)

	if cfg.Tag != "" {
		return repo.Tag(cfg.Tag).Tree(), nil
	}
	if cfg.Branch != "" {
		return repo.Branch(cfg.Branch).Tree(), nil
	}
	if cfg.Commit != "" {
		return repo.Commit(cfg.Commit).Tree(), nil
	}

	return repo.Head().Tree(), nil
}

