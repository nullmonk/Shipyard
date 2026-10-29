package drivers

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"dagger.io/dagger"
)

// RPMDriver implements package building for RPM-based Linux distributions.
type RPMDriver struct {
	Image         string
	topDir        string
	RPMBuildArgs  string
	EnableRepoCmd func(ctr *dagger.Container) *dagger.Container
}

func NewRPMDriver(image string) *RPMDriver {
	return &RPMDriver{
		Image:        image,
		topDir:       "/tmp/build",
		RPMBuildArgs: "",
	}
}

func (r *RPMDriver) MajorVersion() int {
	tag := r.Image
	if idx := strings.LastIndex(tag, ":"); idx >= 0 {
		tag = tag[idx+1:]
	}
	re := regexp.MustCompile(`\d+`)
	m := re.FindString(tag)
	if m != "" {
		if v, err := strconv.Atoi(m); err == nil {
			return v
		}
	}
	return 0
}

func (r *RPMDriver) Setup(ctr *dagger.Container) *dagger.Container {
	if r.EnableRepoCmd != nil {
		ctr = r.EnableRepoCmd(ctr)
	}
	if strings.Contains(strings.ToLower(r.Image), "suse") {
		return ctr
	}
	return ctr.
		WithExec([]string{"yum", "update", "-y"}).
		WithExec([]string{
			"yum", "install", "-y",
			"gcc", "rpmdevtools", "yum-utils", "make",
			"nc", "vim", "git",
		})
}

func (r *RPMDriver) InstallBuildDeps(ctr *dagger.Container, pkg string) *dagger.Container {
	if strings.Contains(strings.ToLower(r.Image), "suse") {
		return ctr.
			WithWorkdir(r.topDir).
			WithExec([]string{"zypper", "-n", "source-install", "-d", pkg}).
			WithExec([]string{"zypper", "-n", "source-install", pkg}).
			WithExec([]string{"/bin/sh", "-c", fmt.Sprintf("rpmbuild %s -bp SOURCES/*.spec || rpmbuild %s -bp SPECS/*.spec", r.RPMBuildArgs, r.RPMBuildArgs)})
	}
	return ctr.
		WithWorkdir(r.topDir).
		WithExec([]string{"yum-builddep", "--skip-broken", "-y", pkg}).
		WithExec([]string{"/bin/sh", "-c", fmt.Sprintf("echo '%%_topdir      %s' > ~/.rpmmacros", r.topDir)}).
		WithExec([]string{"yumdownloader", "--source", pkg}).
		WithExec([]string{"/bin/sh", "-c", "rpm -ivh *.src.rpm"}).
		WithExec([]string{"/bin/sh", "-c", fmt.Sprintf("rpmbuild %s -bp SPECS/*.spec", r.RPMBuildArgs)})
}

func (r *RPMDriver) GetSourceDir(ctx context.Context, ctr *dagger.Container, pkg string) (string, error) {
	build := fmt.Sprintf("%s/BUILD", r.topDir)
	out, err := ctr.WithWorkdir(build).WithExec([]string{"ls", "-F"}).Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("list rpm BUILD directory: %w", err)
	}

	var dirs []string
	for _, l := range strings.Fields(out) {
		if strings.HasSuffix(l, "/") {
			dirs = append(dirs, strings.TrimSuffix(l, "/"))
		}
	}
	if len(dirs) == 0 {
		return build, nil
	}

	name := dirs[0]
	if strings.HasSuffix(name, "-build") {
		innerOut, err := ctr.WithWorkdir(fmt.Sprintf("%s/%s", build, name)).WithExec([]string{"ls", "-F"}).Stdout(ctx)
		if err == nil {
			inner := strings.TrimSuffix(name, "-build")
			for _, f := range strings.Fields(innerOut) {
				if f == inner+"/" {
					return fmt.Sprintf("%s/%s/%s", build, name, inner), nil
				}
			}
		}
	}
	return fmt.Sprintf("%s/%s", build, name), nil
}

func (r *RPMDriver) GetArtifactPattern(pkg string) string { return `.*\.rpm$` }
func (r *RPMDriver) GetArtifactDir() string               { return fmt.Sprintf("%s/RPMS", r.topDir) }
func (r *RPMDriver) DistroName() string                   { return "rpm" }

// InstallArtifactsScript installs the freshly built .rpms (found recursively
// under the RPMS dir) with the distro package manager so dependencies
// resolve from the repos. openSUSE uses zypper (local rpms are unsigned,
// hence --allow-unsigned-rpm); the yum-family prefers dnf with a yum fallback.
func (r *RPMDriver) InstallArtifactsScript() string {
	dir := r.GetArtifactDir()
	if strings.Contains(strings.ToLower(r.Image), "suse") {
		return fmt.Sprintf("found=$(find %s -name '*.rpm' | head -n 1)\n"+
			"if [ -z \"$found\" ]; then echo \"no built .rpm packages in %s\" >&2; exit 1; fi\n"+
			"find %s -name '*.rpm' -exec zypper --non-interactive install --allow-unsigned-rpm {} +", dir, dir, dir)
	}
	return fmt.Sprintf("found=$(find %s -name '*.rpm' | head -n 1)\n"+
		"if [ -z \"$found\" ]; then echo \"no built .rpm packages in %s\" >&2; exit 1; fi\n"+
		"if command -v dnf >/dev/null 2>&1; then find %s -name '*.rpm' -exec dnf install -y {} +; "+
		"else find %s -name '*.rpm' -exec yum install -y {} +; fi", dir, dir, dir, dir)
}
func (r *RPMDriver) TopDir() string { return r.topDir }
func (r *RPMDriver) BuildEnvs() map[string]string {
	if r.RPMBuildArgs != "" {
		return map[string]string{"SHIPYARD_RPM_ARGS": r.RPMBuildArgs}
	}
	return nil
}

// PatchSpecFile injects a shipyard patch declaration into an RPM spec file.
// It is exported so the in-container build command can call it directly.
func PatchSpecFile(content, pkg string, legacyMacro bool) (string, error) {
	lines := strings.Split(content, "\n")

	patchNumRe := regexp.MustCompile(`(?i)^Patch(\d+)\s*:`)
	descRe := regexp.MustCompile(`(?i)^%description`)
	autosetupRe := regexp.MustCompile(`(?i)^%(autosetup|autopatch)`)
	patchRe := regexp.MustCompile(`(?i)^%patch`)
	setupRe := regexp.MustCompile(`(?i)^%(setup|autosetup)`)

	maxNumber := -1
	for _, l := range lines {
		if m := patchNumRe.FindStringSubmatch(strings.TrimSpace(l)); len(m) > 1 {
			if n, err := strconv.Atoi(m[1]); err == nil && n > maxNumber {
				maxNumber = n
			}
		}
	}
	number := maxNumber + 1

	descIdx := -1
	for i, l := range lines {
		if descRe.MatchString(strings.TrimSpace(l)) {
			descIdx = i
			break
		}
	}
	if descIdx < 0 {
		return "", fmt.Errorf("cannot find %%description in the spec for %s", pkg)
	}

	lines = append(lines[:descIdx], append([]string{fmt.Sprintf("Patch%d: %s.patch", number, pkg)}, lines[descIdx:]...)...)

	hasAutosetup := false
	for _, l := range lines {
		if autosetupRe.MatchString(strings.TrimSpace(l)) {
			hasAutosetup = true
			break
		}
	}

	if !hasAutosetup {
		targetIdx := -1
		for i, l := range lines {
			if patchRe.MatchString(strings.TrimSpace(l)) {
				targetIdx = i
			}
		}
		if targetIdx < 0 {
			for i, l := range lines {
				if setupRe.MatchString(strings.TrimSpace(l)) {
					targetIdx = i
				}
			}
		}
		if targetIdx < 0 {
			return "", fmt.Errorf("cannot find %%setup in the spec for %s", pkg)
		}

		patchLine := fmt.Sprintf("%%patch -P %d -p1", number)
		if legacyMacro {
			patchLine = fmt.Sprintf("%%patch%d -p1", number)
		}
		lines = append(lines[:targetIdx+1], append([]string{patchLine}, lines[targetIdx+1:]...)...)
	}

	return strings.Join(lines, "\n") + "\n", nil
}

// IsLegacyRPM reports whether the RPM version string indicates a pre-4.14 release.
func IsLegacyRPM(out string) bool {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return false
	}
	ver := fields[len(fields)-1]
	parts := strings.Split(ver, ".")
	if len(parts) >= 2 {
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		if major < 4 || (major == 4 && minor < 14) {
			return true
		}
	}
	return false
}

// RockyDriver
func NewRockyDriver(image string) *RPMDriver {
	d := NewRPMDriver(image)
	d.EnableRepoCmd = func(ctr *dagger.Container) *dagger.Container {
		repo := "crb"
		if d.MajorVersion() == 8 {
			repo = "powertools"
		}
		return ctr.
			WithExec([]string{"dnf", "install", "-y", "epel-release", "dnf-plugins-core"}).
			WithExec([]string{
				"/bin/sh", "-c",
				fmt.Sprintf("dnf config-manager --enable %s 2>/dev/null || dnf config-manager --set-enabled %s 2>/dev/null || true", repo, repo),
			})
	}
	return d
}

// CentOSDriver
func NewCentOSDriver(image string) *RPMDriver {
	d := NewRPMDriver(image)
	d.EnableRepoCmd = func(ctr *dagger.Container) *dagger.Container {
		isStream := strings.Contains(strings.ToLower(d.Image), "stream")
		major := d.MajorVersion()

		// Legacy CentOS 7/8 (non-stream) reached EOL and repos moved to vault.centos.org.
		// Only run sed if CentOS-* files exist, and only for legacy versions.
		if major > 0 && major <= 8 && !isStream {
			ctr = ctr.WithExec([]string{
				"/bin/sh", "-c",
				`for f in /etc/yum.repos.d/CentOS-*; do ` +
					`[ -f "$f" ] && sed -i -e 's/mirrorlist/#mirrorlist/g' -e 's|#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' "$f"; ` +
					`done || true`,
			})
		}

		if major == 8 {
			ctr = ctr.
				WithExec([]string{"dnf", "install", "-y", "dnf-plugins-core"}).
				WithExec([]string{
					"/bin/sh", "-c",
					`dnf config-manager --enable powertools 2>/dev/null || dnf config-manager --set-enabled powertools 2>/dev/null || true`,
				})
			return ctr
		}

		if major >= 9 || isStream {
			ctr = ctr.WithExec([]string{
				"/bin/sh", "-c",
				`dnf config-manager --enable crb 2>/dev/null || dnf config-manager --set-enabled crb 2>/dev/null || true`,
			})
		}
		return ctr
	}
	return d
}

// SUSEDriver
func NewSUSEDriver(image string) *RPMDriver {
	return &RPMDriver{
		Image:        image,
		topDir:       "/usr/src/packages",
		RPMBuildArgs: "--nodeps",
		EnableRepoCmd: func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithExec([]string{"zypper", "-n", "--gpg-auto-import-keys", "mr", "--enable", "repo-source"}).
				WithExec([]string{"zypper", "-n", "--gpg-auto-import-keys", "refresh"}).
				WithExec([]string{
					"zypper", "-n", "install", "--no-recommends",
					"gcc", "make", "patch", "tar", "gzip", "rpm-build", "git",
					"systemd-rpm-macros", "sysuser-tools",
				})
		},
	}
}
