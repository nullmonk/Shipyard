package shipfile

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"shipyard/pkg/shipyard"
)

// IsValidSourceUrl checks whether a URL is of a recognized good type:
// git repository or archive (.tar.gz, .tgz, .zip, etc.).
func IsValidSourceUrl(rawUrl string) bool {
	u := strings.TrimSpace(rawUrl)
	if u == "" {
		return false
	}

	lower := strings.ToLower(u)

	// Git schemes
	if strings.HasPrefix(lower, "git://") ||
		strings.HasPrefix(lower, "git+http://") ||
		strings.HasPrefix(lower, "git+https://") ||
		strings.HasPrefix(lower, "git+ssh://") ||
		strings.HasPrefix(lower, "git+file://") ||
		strings.HasPrefix(lower, "ssh://git@") {
		return true
	}

	// Git SCP syntax (e.g. git@github.com:user/repo.git)
	if strings.HasPrefix(lower, "git@") {
		return true
	}

	// Parse URL to check host and clean path (without query/fragment)
	path := u
	if parsed, err := url.Parse(u); err == nil {
		if parsed.Path != "" {
			path = parsed.Path
		}
		host := strings.ToLower(parsed.Host)
		if host == "github.com" || strings.HasSuffix(host, ".github.com") ||
			host == "gitlab.com" || strings.HasSuffix(host, ".gitlab.com") ||
			host == "bitbucket.org" || strings.HasSuffix(host, ".bitbucket.org") ||
			host == "codeberg.org" || strings.HasSuffix(host, ".codeberg.org") ||
			strings.HasPrefix(host, "git.") {
			return true
		}
	}

	lowerPath := strings.ToLower(path)
	trimmedPath := strings.TrimSuffix(lowerPath, "/")
	if strings.HasSuffix(trimmedPath, ".git") {
		return true
	}
	if strings.Contains(lowerPath, "/git/") {
		return true
	}

	// Archive extensions
	archiveExts := []string{
		".tar.gz",
		".tgz",
		".zip",
		".tar.xz",
		".txz",
		".tar.bz2",
		".tbz2",
		".tar",
	}
	for _, ext := range archiveExts {
		if strings.HasSuffix(lowerPath, ext) {
			return true
		}
	}

	return false
}

// IsValidSourceURL is an alias for IsValidSourceUrl.
var IsValidSourceURL = IsValidSourceUrl

// Validate checks that the Shipfile complies with all validation rules:
//  1. Source: Shipfiles need at least a source (PackageName or sf.package_name)
//     OR (Url || Urls) with good types (git repo, tar.gz, tgz, zip).
//  2. Patcher: requires at least one codepatch OR a pre/post patch function.
//  3. Builder: requires an artifacts regex and (setup || build).
func (s *Shipfile) Validate() error {
	var errs []string

	// 1. Source requirement
	hasPackage := s.PackageName != "" || s.HasPackageName
	hasUrl := s.Url != ""
	hasUrls := len(s.Urls) > 0

	if !hasPackage && !hasUrl && !hasUrls {
		errs = append(errs, "Shipfile must define at least a source: (PackageName or sf.package_name) or (Url || Urls) with good types (git repo, tar.gz, tgz, zip)")
	}

	if hasUrl {
		if !IsValidSourceUrl(s.Url) {
			errs = append(errs, fmt.Sprintf("invalid Url %q: must be a git repo or archive (.tar.gz, .tgz, .zip)", s.Url))
		}
	}

	if hasUrls {
		for _, u := range s.Urls {
			if !IsValidSourceUrl(u) {
				errs = append(errs, fmt.Sprintf("invalid Url in Urls %q: must be a git repo or archive (.tar.gz, .tgz, .zip)", u))
			}
		}
	}

	// 2. Patcher requirement
	if s.IsPatch() {
		hasPatchLogic := s.HasCodePatches || s.HasPrePatch || s.HasPostPatch
		if !hasPatchLogic {
			errs = append(errs, "Patcher requires at least one codepatch OR a pre/post patch function")
		}
	}

	// 3. Builder requirement
	if s.IsBuild() {
		if s.Artifacts == "" {
			errs = append(errs, "Builder requires an artifacts regex")
		} else if _, err := regexp.Compile(s.Artifacts); err != nil {
			errs = append(errs, fmt.Sprintf("Builder Artifacts regex %q is invalid: %v", s.Artifacts, err))
		}

		hasBuildStep := s.HasSetup || s.HasBuild || s.Setup != "" || s.Build != ""
		if !hasBuildStep {
			errs = append(errs, "Builder requires setup or build function")
		}
	}

	if len(errs) == 1 {
		return fmt.Errorf("shipfile validation failed: %s", errs[0])
	}
	if len(errs) > 1 {
		return fmt.Errorf("shipfile validation failed:\n- %s", strings.Join(errs, "\n- "))
	}

	return nil
}

// Validate loads and validates a shipfile using the provided interpreter.
func Validate(path string, interp *shipyard.Interpreter) (*Shipfile, error) {
	sf, err := Load(path, interp)
	if err != nil {
		return nil, err
	}
	if err := sf.Validate(); err != nil {
		return sf, err
	}
	return sf, nil
}

// ValidateDefault loads and validates a shipfile with default package root.
func ValidateDefault(path string, pkgDir string) (*Shipfile, error) {
	sf, err := LoadDefault(path, pkgDir)
	if err != nil {
		return nil, err
	}
	if err := sf.Validate(); err != nil {
		return sf, err
	}
	return sf, nil
}
