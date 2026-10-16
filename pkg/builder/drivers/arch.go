package drivers

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
)

// ArchDriver implements package building for Arch Linux using pacman, devtools, and makepkg.
type ArchDriver struct {
	Image string
}

func NewArchDriver(image string) *ArchDriver {
	return &ArchDriver{Image: image}
}

func (a *ArchDriver) Setup(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithExec([]string{"pacman", "-Syu", "--noconfirm"}).
		WithExec([]string{"pacman", "-S", "--noconfirm", "base-devel", "git", "devtools", "vim"}).
		WithExec([]string{"useradd", "-m", "builder"}).
		WithExec([]string{"/bin/sh", "-c", "echo 'builder ALL=(ALL) NOPASSWD: ALL' >> /etc/sudoers"}).
		WithExec([]string{"mkdir", "-p", "/tmp/build"}).
		WithExec([]string{"chown", "builder:builder", "/tmp/build"})
}

func (a *ArchDriver) InstallBuildDeps(ctr *dagger.Container, pkg string) *dagger.Container {
	return ctr.
		WithUser("builder").
		WithWorkdir("/tmp/build").
		WithExec([]string{"pkgctl", "repo", "clone", "--protocol", "https", pkg}).
		WithWorkdir(fmt.Sprintf("/tmp/build/%s", pkg)).
		WithExec([]string{"makepkg", "-so", "--noconfirm", "--skippgpcheck"})
}

func (a *ArchDriver) GetSourceDir(ctx context.Context, ctr *dagger.Container, pkg string) (string, error) {
	src := fmt.Sprintf("/tmp/build/%s/src", pkg)
	out, err := ctr.WithWorkdir(src).WithExec([]string{"ls", "-F"}).Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("list arch source directory: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "/") {
			return fmt.Sprintf("%s/%s", src, strings.TrimSuffix(line, "/")), nil
		}
	}
	return src, nil
}

func (a *ArchDriver) GetArtifactPattern(pkg string) string { return `.*\.pkg\.tar\.zst$` }
func (a *ArchDriver) GetArtifactDir() string               { return "/tmp/build/artifacts" }
func (a *ArchDriver) DistroName() string                   { return "arch" }
func (a *ArchDriver) TopDir() string                       { return "/tmp/build" }
func (a *ArchDriver) BuildEnvs() map[string]string         { return nil }
