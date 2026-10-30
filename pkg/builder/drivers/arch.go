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

// InstallArtifactsScript installs the freshly built packages with pacman.
// The build runs as the unprivileged builder user, so sudo (NOPASSWD from
// Setup) escalates for the install.
func (a *ArchDriver) InstallArtifactsScript() string {
	dir := a.GetArtifactDir()
	return fmt.Sprintf("set -- %s/*.pkg.tar.zst\n"+
		"if [ ! -e \"$1\" ]; then echo \"no built packages in %s\" >&2; exit 1; fi\n"+
		"sudo pacman -U --noconfirm \"$@\"", dir, dir)
}

// InstallPackageScript installs one artifact file (existing path or
// *.pkg.tar.* name) with pacman -U, or a package name from the repos with
// pacman -S. sudo (NOPASSWD from Setup) escalates when tests run as the
// unprivileged builder user; running as root falls back to plain pacman.
func (a *ArchDriver) InstallPackageScript(target string) string {
	t := shQuote(target)
	return `t=` + t + `
case "$t" in *.pkg.tar.*) :;; *) if [ ! -e "$t" ]; then if command -v sudo >/dev/null 2>&1; then sudo pacman -S --noconfirm --needed "$t"; else pacman -S --noconfirm --needed "$t"; fi; exit $?; fi;; esac
` +
		`if command -v sudo >/dev/null 2>&1; then sudo pacman -U --noconfirm --needed "$t"; else pacman -U --noconfirm --needed "$t"; fi`
}
func (a *ArchDriver) DistroName() string           { return "arch" }
func (a *ArchDriver) TopDir() string               { return "/tmp/build" }
func (a *ArchDriver) BuildEnvs() map[string]string { return nil }
