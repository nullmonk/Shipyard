package drivers

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
)

// DistroDriver defines the lifecycle methods needed to prepare, patch,
// and build OS packages inside a distro container using Dagger.
type DistroDriver interface {
	// Setup performs initial setup for the distro (repos, build tools).
	Setup(ctr *dagger.Container) *dagger.Container

	// InstallBuildDeps installs build dependencies and fetches/unpacks pristine source.
	InstallBuildDeps(ctr *dagger.Container, pkg string) *dagger.Container

	// PrepareSource prepares the source code if needed.
	PrepareSource(ctr *dagger.Container, pkg string) *dagger.Container

	// GetSourceDir returns the directory inside the container where pristine source is located.
	GetSourceDir(ctx context.Context, ctr *dagger.Container, pkg string) (string, error)

	// ApplyPatch applies the generated patch to the package the distro's way.
	ApplyPatch(ctx context.Context, ctr *dagger.Container, patchContent string, pkg string) (*dagger.Container, error)

	// Build executes the package build command.
	Build(ctr *dagger.Container, pkg string) *dagger.Container

	// GetArtifactPattern returns the default regex pattern matching built packages.
	GetArtifactPattern(pkg string) string

	// GetArtifactDir returns the directory where built artifacts reside.
	GetArtifactDir() string
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
	if strings.Contains(img, "rocky") {
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

