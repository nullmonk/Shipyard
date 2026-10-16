package incontainer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"shipyard/pkg/builder/drivers"
)

func newApplyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Generate diff from CodePatch changes and import it into the distro build system",
		Long: `Apply generates a unified diff of CodePatch changes, resets the source to
pristine, then imports the patch into the distro's packaging system
(quilt for Debian, spec + SOURCES for RPM, APKBUILD for Alpine, patch for Arch).`,
		Args: cobra.NoArgs,
		RunE: runApply,
	}
}

func runApply(_ *cobra.Command, _ []string) error {
	distro := os.Getenv("SHIPYARD_DISTRO")
	if distro == "" {
		return fmt.Errorf("SHIPYARD_DISTRO is not set; apply is only for distro package builds")
	}

	sourceDir := os.Getenv("SHIPYARD_SOURCE")
	topDir := os.Getenv("SHIPYARD_TOPDIR")
	pkgName := os.Getenv("SHIPYARD_PACKAGE")
	pristine := os.Getenv("SHIPYARD_PRISTINE")

	if sourceDir == "" {
		return fmt.Errorf("SHIPYARD_SOURCE is not set")
	}
	if pristine == "" {
		return fmt.Errorf("SHIPYARD_PRISTINE is not set")
	}

	// Generate unified diff. diff exits 1 when differences exist (expected);
	// '; true' ignores that so the script always succeeds.
	fmt.Println("[*] Generating patch from CodePatch changes...")
	parent := filepath.Dir(sourceDir)
	base := filepath.Base(sourceDir)
	diffScript := fmt.Sprintf(
		"cp -a '%s' .shipyard-diff && diff -ruN .shipyard-diff '%s' > /tmp/shipyard.patch; true; rm -rf .shipyard-diff",
		pristine, base)
	if err := shellIn(parent, diffScript); err != nil {
		return fmt.Errorf("generate patch: %w", err)
	}

	// Reset the source tree to pristine.
	fmt.Println("[*] Resetting source to pristine...")
	if err := shellIn("/", fmt.Sprintf("rm -rf '%s' && cp -a '%s' '%s'", sourceDir, pristine, sourceDir)); err != nil {
		return fmt.Errorf("reset source: %w", err)
	}

	patchBytes, _ := os.ReadFile("/tmp/shipyard.patch")
	hasPatch := len(strings.TrimSpace(string(patchBytes))) > 0

	fmt.Printf("[*] Importing patch for %s...\n", distro)
	switch distro {
	case "debian":
		return applyDebianPatch(topDir, sourceDir, hasPatch)
	case "rpm":
		return applyRPMPatch(topDir, pkgName, hasPatch)
	case "alpine":
		return applyAlpinePatch(topDir, pkgName, hasPatch)
	case "arch":
		return applyArchPatch(sourceDir, hasPatch)
	default:
		return fmt.Errorf("unsupported distro %q", distro)
	}
}

func applyDebianPatch(topDir, sourceDir string, hasPatch bool) error {
	if !hasPatch {
		return nil
	}
	if err := shellIn(topDir, "quilt import /tmp/shipyard.patch"); err != nil {
		return fmt.Errorf("quilt import: %w", err)
	}
	if err := shellIn(sourceDir, "quilt push && quilt refresh"); err != nil {
		return fmt.Errorf("quilt push: %w", err)
	}
	return nil
}

func applyRPMPatch(topDir, pkgName string, hasPatch bool) error {
	if !hasPatch {
		return nil
	}
	specsDir := filepath.Join(topDir, "SPECS")
	entries, err := os.ReadDir(specsDir)
	if err != nil {
		return fmt.Errorf("read SPECS dir: %w", err)
	}
	var specFile string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".spec") {
			specFile = e.Name()
			break
		}
	}
	if specFile == "" {
		return fmt.Errorf("no .spec file found in %s", specsDir)
	}
	specPath := filepath.Join(specsDir, specFile)
	content, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read spec file: %w", err)
	}
	out, _ := exec.Command("rpm", "--version").Output()
	legacy := drivers.IsLegacyRPM(string(out))
	newSpec, err := drivers.PatchSpecFile(string(content), pkgName, legacy)
	if err != nil {
		return fmt.Errorf("patch spec: %w", err)
	}
	if err := os.WriteFile(specPath, []byte(newSpec), 0o644); err != nil {
		return fmt.Errorf("write spec: %w", err)
	}
	patchBytes, _ := os.ReadFile("/tmp/shipyard.patch")
	if err := os.WriteFile(filepath.Join(topDir, "SOURCES", pkgName+".patch"), patchBytes, 0o644); err != nil {
		return fmt.Errorf("copy patch to SOURCES: %w", err)
	}
	return nil
}

func applyAlpinePatch(topDir, pkgName string, hasPatch bool) error {
	if !hasPatch {
		return nil
	}
	pkgDir := filepath.Join(topDir, pkgName)
	patchBytes, _ := os.ReadFile("/tmp/shipyard.patch")
	if err := os.WriteFile(filepath.Join(pkgDir, "shipyard.patch"), patchBytes, 0o644); err != nil {
		return fmt.Errorf("copy patch: %w", err)
	}
	if err := shellIn(pkgDir, `sed -i 's/^source="/source="\n\tshipyard.patch\n\t/' APKBUILD`); err != nil {
		return fmt.Errorf("patch APKBUILD source: %w", err)
	}
	if err := shellIn(pkgDir, `echo 'options="$options !check"' >> APKBUILD`); err != nil {
		return fmt.Errorf("patch APKBUILD options: %w", err)
	}
	if err := shellIn(pkgDir, "abuild -F checksum"); err != nil {
		return fmt.Errorf("abuild checksum: %w", err)
	}
	return nil
}

func applyArchPatch(sourceDir string, hasPatch bool) error {
	if !hasPatch {
		return nil
	}
	if err := shellIn(sourceDir, "patch -p1 < /tmp/shipyard.patch"); err != nil {
		return fmt.Errorf("apply patch: %w", err)
	}
	if err := shellIn(sourceDir, "find . -exec touch -h -d @0 {} +"); err != nil {
		return fmt.Errorf("touch timestamps: %w", err)
	}
	return nil
}
