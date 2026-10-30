package drivers

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
)

// DistroDriver defines the lifecycle methods needed to prepare and build
// OS packages inside a distro container using Dagger.
type DistroDriver interface {
	// Setup performs initial setup for the distro (repos, build tools).
	Setup(ctr *dagger.Container) *dagger.Container

	// InstallBuildDeps installs build dependencies and fetches/unpacks pristine source.
	InstallBuildDeps(ctr *dagger.Container, pkg string) *dagger.Container

	// GetSourceDir returns the directory inside the container where pristine source is located.
	GetSourceDir(ctx context.Context, ctr *dagger.Container, pkg string) (string, error)

	// GetArtifactPattern returns the default regex pattern matching built packages.
	GetArtifactPattern(pkg string) string

	// GetArtifactDir returns the directory where built artifacts reside.
	GetArtifactDir() string

	// DistroName returns a short identifier for the distro family (e.g. "debian", "rpm", "alpine", "arch").
	DistroName() string

	// TopDir returns the top-level build directory inside the container.
	TopDir() string

	// BuildEnvs returns any additional environment variables the in-container build needs.
	BuildEnvs() map[string]string

	// InstallArtifactsScript returns a POSIX shell snippet that installs every
	// package artifact in GetArtifactDir() with the distro package manager.
	// BuildPackage runs it after building and before @test functions, so tests
	// exercise the newly built packages rather than distro-shipped versions.
	// Tests may therefore assume the new package is installed. The snippet
	// exits non-zero when no artifacts are found or installation fails.
	InstallArtifactsScript() string

	// InstallPackageScript returns a POSIX shell snippet that installs a
	// single target: either a package file (an existing path, or a name with
	// a package suffix) with the native manager so dependencies resolve, or
	// a package name from the configured repos/mirrors (including the
	// drydock hosted mirror). The snippet exits non-zero on failure.
	// Tests use it via the InstallPackage shipfile helper to install a
	// specific artifact file or repo package directly.
	InstallPackageScript(target string) string
}

// shQuote single-quotes s for embedding in a POSIX shell snippet.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// GetDriver selects the appropriate DistroDriver for the given container image.
func GetDriver(image string) (DistroDriver, error) {
	img := strings.ToLower(image)

	for _, name := range []string{"debian", "ubuntu", "linuxmint", "kali"} {
		if strings.Contains(img, name) {
			return NewDebianDriver(image), nil
		}
	}
	if strings.Contains(img, "suse") {
		return NewSUSEDriver(image), nil
	}
	if strings.Contains(img, "alpine") {
		return NewAlpineDriver(image), nil
	}
	if strings.Contains(img, "rocky") || strings.Contains(img, "alma") {
		return NewRockyDriver(image), nil
	}
	if strings.Contains(img, "centos") {
		return NewCentOSDriver(image), nil
	}
	for _, name := range []string{"redhat", "fedora", "amazonlinux", "rhel"} {
		if strings.Contains(img, name) {
			return NewRPMDriver(image), nil
		}
	}
	if strings.Contains(img, "archlinux") {
		return NewArchDriver(image), nil
	}

	return nil, fmt.Errorf("unsupported distro image %q", image)
}
