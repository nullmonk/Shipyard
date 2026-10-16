package drivers

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
)

// DebianDriver implements package building for Debian and Ubuntu based distros.
type DebianDriver struct {
	Image string
}

func NewDebianDriver(image string) *DebianDriver {
	return &DebianDriver{Image: image}
}

func (d *DebianDriver) Setup(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithExec([]string{"ln", "-fs", "/usr/share/zoneinfo/America/New_York", "/etc/localtime"}).
		WithExec([]string{
			"/bin/bash", "-c",
			`. /etc/os-release; ` +
				`case "${VERSION_CODENAME:-}" in ` +
				`  buster|stretch|jessie|wheezy|squeeze) ` +
				`    printf 'deb http://archive.debian.org/debian %s main\n` +
				`deb-src http://archive.debian.org/debian %s main\n` +
				`deb http://archive.debian.org/debian-security %s/updates main\n` +
				`deb-src http://archive.debian.org/debian-security %s/updates main\n' ` +
				`      "$VERSION_CODENAME" "$VERSION_CODENAME" ` +
				`      "$VERSION_CODENAME" "$VERSION_CODENAME" > /etc/apt/sources.list; ` +
				`    echo 'Acquire::Check-Valid-Until "false";' ` +
				`      > /etc/apt/apt.conf.d/99no-check-valid-until; ` +
				`    find /etc/apt/sources.list.d -type f -delete 2>/dev/null || true; ` +
				`    ;; ` +
				`esac`,
		}).
		WithExec([]string{
			"/bin/bash", "-c",
			`if [ -f /etc/apt/sources.list ]; then ` +
				`  sed -i 's/# deb-src/deb-src/' /etc/apt/sources.list; ` +
				`  if ! grep -q 'deb-src' /etc/apt/sources.list; then ` +
				`    src=$(grep -E '^\s*deb ' /etc/apt/sources.list | head -n 1); ` +
				`    if [ -n "$src" ]; then echo "${src/deb /deb-src }" >> /etc/apt/sources.list; fi; ` +
				`  fi; ` +
				`fi`,
		}).
		WithExec([]string{"find", "/etc/apt/sources.list.d", "-type", "f", "-exec", "sed", "-i", "s/Types:/Types: deb-src/", "{}", ";"}).
		WithExec([]string{"apt-get", "update", "-qq"}).
		WithExec([]string{
			"apt-get", "install", "-qq", "-y",
			"gcc", "devscripts", "quilt", "build-essential",
			"vim", "iproute2", "nmap", "git",
		}).
		WithExec([]string{"mkdir", "-p", "/tmp/build/"})
}

func (d *DebianDriver) InstallBuildDeps(ctr *dagger.Container, pkg string) *dagger.Container {
	return ctr.
		WithWorkdir("/tmp/build").
		WithExec([]string{"apt-get", "build-dep", "-q", "-y", pkg}).
		WithExec([]string{"/bin/sh", "-c", fmt.Sprintf("(apt-get install -q -y %s || echo -n)", pkg)}).
		WithExec([]string{"apt-get", "source", "-qq", pkg})
}

func (d *DebianDriver) PrepareSource(ctr *dagger.Container, pkg string) *dagger.Container {
	return ctr
}

func (d *DebianDriver) GetSourceDir(ctx context.Context, ctr *dagger.Container, pkg string) (string, error) {
	out, err := ctr.WithWorkdir("/tmp/build").WithExec([]string{"ls", "-F"}).Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("list debian source directory: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "/") && line != "artifacts/" {
			return "/tmp/build/" + strings.TrimSuffix(line, "/"), nil
		}
	}
	return "/tmp/build", nil
}

func (d *DebianDriver) ApplyPatch(ctx context.Context, ctr *dagger.Container, patchContent string, pkg string) (*dagger.Container, error) {
	return ctr.
		WithNewFile("/tmp/build/shipyard.patch", patchContent).
		WithWorkdir("/tmp/build").
		WithExec([]string{"/bin/bash", "-c", "cd */ && quilt import /tmp/build/shipyard.patch"}).
		WithExec([]string{"/bin/bash", "-c", "cd */ && quilt push"}).
		WithExec([]string{"/bin/bash", "-c", "cd */ && quilt refresh"}), nil
}

func (d *DebianDriver) Build(ctr *dagger.Container, pkg string) *dagger.Container {
	return ctr.
		WithEnvVariable("DEB_BUILD_OPTIONS", "notest nocheck").
		WithEnvVariable("DEBUILD_DPKG_BUILDPACKAGE_OPTS", "-d").
		WithWorkdir("/tmp/build").
		WithExec([]string{"/bin/bash", "-c", "cd */ && debuild --no-lintian -d -uc -us -b"})
}

func (d *DebianDriver) GetArtifactPattern(pkg string) string {
	return `.*\.deb$`
}

func (d *DebianDriver) GetArtifactDir() string {
	return "/tmp/build"
}

