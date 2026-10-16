package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
		if len(exclude) == 0 {
			exclude = []string{".git/", "__pycache__/", "build-output/", "sources/"}
		}

		return client.Host().Directory(abs, dagger.HostDirectoryOpts{
			Exclude: exclude,
		}), nil
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

