package drivers

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
)

const aportsGit = "https://gitlab.alpinelinux.org/alpine/aports.git"

// AlpineDriver implements package building for Alpine Linux using aports and abuild.
type AlpineDriver struct {
	Image string
}

func NewAlpineDriver(image string) *AlpineDriver {
	return &AlpineDriver{Image: image}
}

func (a *AlpineDriver) Setup(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithExec([]string{"apk", "add", "--no-cache", "alpine-sdk", "git", "patch"}).
		WithExec([]string{"abuild-keygen", "-a", "-n"}).
		WithExec([]string{"/bin/sh", "-c", "cp /root/.abuild/*.pub /etc/apk/keys/"}).
		WithExec([]string{"mkdir", "-p", "/tmp/build"})
}

func (a *AlpineDriver) InstallBuildDeps(ctr *dagger.Container, pkg string) *dagger.Container {
	getAport := fmt.Sprintf(
		`rel=$(cat /etc/alpine-release); `+
			`case "$rel" in *_*|*edge*) branch=master;; *) branch=$(echo "$rel" | cut -d. -f1,2)-stable;; esac; `+
			`git clone -q --depth 1 --branch "$branch" --filter=blob:none --sparse %s /tmp/aports && `+
			`cd /tmp/aports && `+
			`git sparse-checkout set main/%s community/%s testing/%s && `+
			`cp -r $(ls -d main/%s community/%s testing/%s 2>/dev/null | head -n 1) /tmp/build/%s`,
		aportsGit, pkg, pkg, pkg, pkg, pkg, pkg, pkg,
	)

	return ctr.
		WithExec([]string{"/bin/sh", "-c", getAport}).
		WithWorkdir(fmt.Sprintf("/tmp/build/%s", pkg)).
		WithExec([]string{"abuild", "-F", "deps"}).
		WithExec([]string{"abuild", "-F", "fetch", "unpack", "prepare"})
}

func (a *AlpineDriver) PrepareSource(ctr *dagger.Container, pkg string) *dagger.Container {
	return ctr
}

func (a *AlpineDriver) GetSourceDir(ctx context.Context, ctr *dagger.Container, pkg string) (string, error) {
	src := fmt.Sprintf("/tmp/build/%s/src", pkg)
	out, err := ctr.WithWorkdir(src).WithExec([]string{"ls", "-F"}).Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("list alpine source directory: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "/") {
			return fmt.Sprintf("%s/%s", src, strings.TrimSuffix(line, "/")), nil
		}
	}
	return src, nil
}

func (a *AlpineDriver) ApplyPatch(ctx context.Context, ctr *dagger.Container, patchContent string, pkg string) (*dagger.Container, error) {
	pkgDir := fmt.Sprintf("/tmp/build/%s", pkg)
	patchPath := fmt.Sprintf("%s/shipyard.patch", pkgDir)

	return ctr.
		WithWorkdir(pkgDir).
		WithNewFile(patchPath, patchContent).
		WithExec([]string{"sed", "-i", `s/^source="/source="\n\tshipyard.patch\n\t/`, "APKBUILD"}).
		WithExec([]string{"/bin/sh", "-c", `echo 'options="$options !check"' >> APKBUILD`}).
		WithExec([]string{"abuild", "-F", "checksum"}), nil
}

func (a *AlpineDriver) Build(ctr *dagger.Container, pkg string) *dagger.Container {
	return ctr.
		WithWorkdir(fmt.Sprintf("/tmp/build/%s", pkg)).
		WithExec([]string{"abuild", "-F", "-r"})
}

func (a *AlpineDriver) GetArtifactPattern(pkg string) string {
	return `.*\.apk$`
}

func (a *AlpineDriver) GetArtifactDir() string {
	return "/root/packages"
}

