package shipfile

import "fmt"

// Dependency describes a build-time or run-time package requirement.
// Package names vary by distro (e.g. "libssl-dev" on Debian vs "openssl-devel"
// on Fedora), so Names maps distro IDs to the correct package name.
// The empty key "" is the default/fallback name used when no distro-specific
// entry is present.
type Dependency struct {
	// Names maps a distro id (matching Distro.ID or Distro.IDLike) to the
	// package name as that distro's package manager knows it.
	// Use "" as the fallback key.
	//   e.g. {"": "openssl", "debian": "libssl-dev", "rhel": "openssl-devel"}
	Names map[string]string

	// Version constraint on the required package, e.g. "1.1.0".
	// Empty means any version is acceptable.
	Version Version

	// Op is the comparison operator for the version constraint.
	// One of: "=", "==", "!=", "<", "<=", ">", ">=".
	// Ignored when Version is empty.
	Op string
}

// NameFor returns the package name to use for the given distro ID.
// It checks the distro ID first, then falls back to "" (the default).
// Returns an error only if neither key is present.
func (d Dependency) NameFor(distroID string) (string, error) {
	if name, ok := d.Names[distroID]; ok {
		return name, nil
	}
	if name, ok := d.Names[""]; ok {
		return name, nil
	}
	return "", fmt.Errorf("dependency has no name for distro %q and no default", distroID)
}

// Constraint returns the version constraint string, e.g. ">= 1.1.0".
// Returns "" when no version constraint is set.
func (d Dependency) Constraint() string {
	if d.Version == "" {
		return ""
	}
	return d.Op + " " + string(d.Version)
}

// String returns a human-readable description using the default package name.
func (d Dependency) String() string {
	name, _ := d.NameFor("")
	c := d.Constraint()
	if c == "" {
		return name
	}
	return name + " (" + c + ")"
}
