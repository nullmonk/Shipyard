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
	TopDir        string
	RPMBuildArgs  string
	EnableRepoCmd func(ctr *dagger.Container) *dagger.Container
}

func NewRPMDriver(image string) *RPMDriver {
	return &RPMDriver{
		Image:        image,
		TopDir:       "/tmp/build",
		RPMBuildArgs: "",
	}
}

func (r *RPMDriver) MajorVersion() int {
	re := regexp.MustCompile(`:(\d+)`)
	m := re.FindStringSubmatch(r.Image)
	if len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil {
			return v
		}
	}
	return 0
}

func (r *RPMDriver) Setup(ctr *dagger.Container) *dagger.Container {
	if r.EnableRepoCmd != nil {
		ctr = r.EnableRepoCmd(ctr)
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
	return ctr.
		WithWorkdir(r.TopDir).
		WithExec([]string{"yum-builddep", "--skip-broken", "-y", pkg}).
		WithExec([]string{"/bin/sh", "-c", fmt.Sprintf("echo '%%_topdir      %s' > ~/.rpmmacros", r.TopDir)}).
		WithExec([]string{"yumdownloader", "--source", pkg}).
		WithExec([]string{"/bin/sh", "-c", "rpm -ivh *.src.rpm"}).
		WithExec([]string{"/bin/sh", "-c", fmt.Sprintf("rpmbuild %s -bp SPECS/*.spec", r.RPMBuildArgs)})
}

func (r *RPMDriver) PrepareSource(ctr *dagger.Container, pkg string) *dagger.Container {
	return ctr
}

func (r *RPMDriver) GetSourceDir(ctx context.Context, ctr *dagger.Container, pkg string) (string, error) {
	build := fmt.Sprintf("%s/BUILD", r.TopDir)
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

func (r *RPMDriver) ApplyPatch(ctx context.Context, ctr *dagger.Container, patchContent string, pkg string) (*dagger.Container, error) {
	entries, err := ctr.Directory(fmt.Sprintf("%s/SPECS", r.TopDir)).Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("read SPECS entries: %w", err)
	}

	var specFile string
	for _, e := range entries {
		if strings.HasSuffix(e, ".spec") {
			specFile = e
			break
		}
	}
	if specFile == "" {
		return nil, fmt.Errorf("no SPEC file found in %s/SPECS", r.TopDir)
	}

	specPath := fmt.Sprintf("%s/SPECS/%s", r.TopDir, specFile)
	content, err := ctr.File(specPath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read spec file contents: %w", err)
	}

	rpmVerOut, err := ctr.WithExec([]string{"rpm", "--version"}).Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("rpm version: %w", err)
	}

	legacyMacro := isLegacyRPM(rpmVerOut)
	newSpec, err := patchSpecFile(content, pkg, legacyMacro)
	if err != nil {
		return nil, err
	}

	return ctr.
		WithNewFile(specPath, newSpec).
		WithNewFile(fmt.Sprintf("%s/SOURCES/%s.patch", r.TopDir, pkg), patchContent), nil
}

func (r *RPMDriver) Build(ctr *dagger.Container, pkg string) *dagger.Container {
	return ctr.
		WithWorkdir(r.TopDir).
		WithExec([]string{"/bin/sh", "-c", fmt.Sprintf("rpmbuild --define 'debug_package %%{nil}' --nocheck %s -bb SPECS/*.spec", r.RPMBuildArgs)})
}

func (r *RPMDriver) GetArtifactPattern(pkg string) string {
	return `.*\.rpm$`
}

func (r *RPMDriver) GetArtifactDir() string {
	return fmt.Sprintf("%s/RPMS", r.TopDir)
}

func isLegacyRPM(out string) bool {
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

func patchSpecFile(content, pkg string, legacyMacro bool) (string, error) {
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
			WithExec([]string{"dnf", "config-manager", "--set-enabled", repo})
	}
	return d
}

// CentOSDriver
func NewCentOSDriver(image string) *RPMDriver {
	d := NewRPMDriver(image)
	d.EnableRepoCmd = func(ctr *dagger.Container) *dagger.Container {
		ctr = ctr.WithExec([]string{
			"/bin/sh", "-c",
			`sed -i -e 's/mirrorlist/#mirrorlist/g' ` +
				`-e 's|#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' ` +
				`/etc/yum.repos.d/CentOS-*`,
		})
		if d.MajorVersion() == 8 {
			ctr = ctr.
				WithExec([]string{"dnf", "install", "-y", "dnf-plugins-core"}).
				WithExec([]string{"dnf", "config-manager", "--set-enabled", "powertools"})
		}
		return ctr
	}
	return d
}

// SUSEDriver
func NewSUSEDriver(image string) *RPMDriver {
	return &RPMDriver{
		Image:        image,
		TopDir:       "/usr/src/packages",
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

