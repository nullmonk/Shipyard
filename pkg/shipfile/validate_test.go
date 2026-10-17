package shipfile_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"shipyard/pkg/shipfile"
)

func loadShipfileContent(t *testing.T, content string) *shipfile.Shipfile {
	t.Helper()
	dir, err := os.MkdirTemp("", "shipfile-val-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sfPath := filepath.Join(dir, "shipfile.py")
	if err := os.WriteFile(sfPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	sf, err := shipfile.LoadDefault(sfPath, root)
	if err != nil {
		t.Fatalf("failed to load shipfile: %v", err)
	}
	return sf
}

func TestValidateSourceRequirements(t *testing.T) {
	// 1. Missing source (no PackageName, no Url, no Urls)
	sfNoSource := loadShipfileContent(t, `
from shipyard import Shipfile

class Shipfile:
    Name = "test"
`)
	if err := sfNoSource.Validate(); err == nil {
		t.Errorf("expected validation error for missing source, got nil")
	}

	// 2. Valid with PackageName
	sfPkg := loadShipfileContent(t, `
from shipyard import Shipfile

class Shipfile:
    Name = "test"
    PackageName = "test-pkg"
`)
	if err := sfPkg.Validate(); err != nil {
		t.Errorf("expected valid for PackageName, got: %v", err)
	}

	// 3. Valid with package_name method override
	sfPkgMethod := loadShipfileContent(t, `
from shipyard import Shipfile

class Shipfile:
    Name = "test"
    def package_name(self, ctx=None):
        return "custom-pkg"
`)
	if err := sfPkgMethod.Validate(); err != nil {
		t.Errorf("expected valid for package_name method, got: %v", err)
	}

	// 4. Valid with Url (git repo)
	sfGitUrl := loadShipfileContent(t, `
from shipyard import Shipfile

class Shipfile:
    Name = "test"
    Url = "https://github.com/foo/bar"
`)
	if err := sfGitUrl.Validate(); err != nil {
		t.Errorf("expected valid for git Url, got: %v", err)
	}

	// 5. Valid with Url (archive tar.gz)
	sfTarUrl := loadShipfileContent(t, `
from shipyard import Shipfile

class Shipfile:
    Name = "test"
    Url = "https://example.com/foo-1.0.tar.gz"
`)
	if err := sfTarUrl.Validate(); err != nil {
		t.Errorf("expected valid for tar.gz Url, got: %v", err)
	}

	// 6. Valid with Urls (list of valid archive URLs)
	sfUrls := loadShipfileContent(t, `
from shipyard import Shipfile

class Shipfile:
    Name = "test"
    Urls = ["https://example.com/foo-1.0.tar.gz", "https://example.com/foo-2.0.zip"]
`)
	if err := sfUrls.Validate(); err != nil {
		t.Errorf("expected valid for Urls list, got: %v", err)
	}

	// 7. Invalid Url type
	sfBadUrl := loadShipfileContent(t, `
from shipyard import Shipfile

class Shipfile:
    Name = "test"
    Url = "https://example.com/foo.exe"
`)
	if err := sfBadUrl.Validate(); err == nil {
		t.Errorf("expected error for bad Url type, got nil")
	}

	// 8. Invalid Url in Urls list
	sfBadUrls := loadShipfileContent(t, `
from shipyard import Shipfile

class Shipfile:
    Name = "test"
    Urls = ["https://example.com/foo-1.0.tar.gz", "https://example.com/foo.exe"]
`)
	if err := sfBadUrls.Validate(); err == nil {
		t.Errorf("expected error for bad Url in Urls list, got nil")
	}
}

func TestValidatePatcherRequirements(t *testing.T) {
	// 1. Patcher with CodePatch
	sfWithCodePatch := loadShipfileContent(t, `
from shipyard import Patcher, CodePatch

class Shipfile(Patcher):
    Name = "test"
    PackageName = "test-pkg"

    @CodePatch(r".*\.c$")
    def patch_it(file, version):
        pass
`)
	if err := sfWithCodePatch.Validate(); err != nil {
		t.Errorf("expected valid for Patcher with CodePatch, got: %v", err)
	}

	// 2. Patcher with pre_patch method
	sfWithPrePatch := loadShipfileContent(t, `
from shipyard import Patcher

class Shipfile(Patcher):
    Name = "test"
    PackageName = "test-pkg"

    def pre_patch(self, ctx):
        pass
`)
	if err := sfWithPrePatch.Validate(); err != nil {
		t.Errorf("expected valid for Patcher with pre_patch, got: %v", err)
	}

	// 3. Patcher with post_patch method
	sfWithPostPatch := loadShipfileContent(t, `
from shipyard import Patcher

class Shipfile(Patcher):
    Name = "test"
    PackageName = "test-pkg"

    def post_patch(self, ctx):
        pass
`)
	if err := sfWithPostPatch.Validate(); err != nil {
		t.Errorf("expected valid for Patcher with post_patch, got: %v", err)
	}

	// 4. Patcher without CodePatch or pre/post patch (should fail)
	sfEmptyPatcher := loadShipfileContent(t, `
from shipyard import Patcher

class Shipfile(Patcher):
    Name = "test"
    PackageName = "test-pkg"
`)
	if err := sfEmptyPatcher.Validate(); err == nil {
		t.Errorf("expected error for Patcher without patch logic, got nil")
	}
}

func TestValidateBuilderRequirements(t *testing.T) {
	// 1. Valid Builder with Artifacts and build() method
	sfBuildScript := loadShipfileContent(t, `
from shipyard import Builder

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"

    def build(self, ctx):
        pass
`)
	if err := sfBuildScript.Validate(); err != nil {
		t.Errorf("expected valid for Builder with Build, got: %v", err)
	}

	// 2. Valid Builder with Artifacts and setup() method
	sfSetupScript := loadShipfileContent(t, `
from shipyard import Builder

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"

    def setup(self, ctx):
        pass
`)
	if err := sfSetupScript.Validate(); err != nil {
		t.Errorf("expected valid for Builder with Setup, got: %v", err)
	}

	// 3. Valid Builder with build method
	sfBuildMethod := loadShipfileContent(t, `
from shipyard import Builder

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"

    def build(self, ctx):
        pass
`)
	if err := sfBuildMethod.Validate(); err != nil {
		t.Errorf("expected valid for Builder with build method, got: %v", err)
	}

	// 4. Valid Builder with setup method
	sfSetupMethod := loadShipfileContent(t, `
from shipyard import Builder

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"

    def setup(self, ctx):
        pass
`)
	if err := sfSetupMethod.Validate(); err != nil {
		t.Errorf("expected valid for Builder with setup method, got: %v", err)
	}

	// 5. Invalid Builder: missing Artifacts
	sfNoArtifacts := loadShipfileContent(t, `
from shipyard import Builder

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"

    def build(self, ctx):
        pass
`)
	if err := sfNoArtifacts.Validate(); err == nil {
		t.Errorf("expected error for Builder missing Artifacts, got nil")
	}

	// 6. Invalid Builder: invalid regex in Artifacts
	sfBadRegex := loadShipfileContent(t, `
from shipyard import Builder

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"[a-z("

    def build(self, ctx):
        pass
`)
	if err := sfBadRegex.Validate(); err == nil {
		t.Errorf("expected error for Builder with invalid regex, got nil")
	}

	// 7. Invalid Builder: neither setup nor build
	sfNoStep := loadShipfileContent(t, `
from shipyard import Builder

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"
`)
	if err := sfNoStep.Validate(); err == nil {
		t.Errorf("expected error for Builder without setup or build, got nil")
	}
}

func TestValidateCombinedPatcherAndBuilder(t *testing.T) {
	// Subclasses both Patcher and Builder, satisfies both
	sfBothValid := loadShipfileContent(t, `
from shipyard import Patcher, Builder, CodePatch

class Shipfile(Patcher, Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"

    @CodePatch(r".*\.c$")
    def patch_it(file, version):
        pass

    def build(self, ctx):
        pass
`)
	if err := sfBothValid.Validate(); err != nil {
		t.Errorf("expected valid for dual Patcher and Builder, got: %v", err)
	}

	// Subclasses both, but missing Patcher logic
	sfMissingPatch := loadShipfileContent(t, `
from shipyard import Patcher, Builder

class Shipfile(Patcher, Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"

    def build(self, ctx):
        pass
`)
	if err := sfMissingPatch.Validate(); err == nil {
		t.Errorf("expected error for dual class missing patch logic, got nil")
	}

	// Subclasses both, but missing Builder step
	sfMissingBuild := loadShipfileContent(t, `
from shipyard import Patcher, Builder, CodePatch

class Shipfile(Patcher, Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"

    @CodePatch(r".*\.c$")
    def patch_it(file, version):
        pass
`)
	if err := sfMissingBuild.Validate(); err == nil {
		t.Errorf("expected error for dual class missing build step, got nil")
	}
}

func TestValidateVariables(t *testing.T) {
	// 1. Valid variable
	sfValid := loadShipfileContent(t, `
from shipyard import Builder, Variable

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"
    def build(self, ctx): pass

    Variables = [
        Variable("MY_VAR", "My description", "valid_string"),
    ]
`)
	if err := sfValid.Validate(); err != nil {
		t.Errorf("expected valid variable to pass, got: %v", err)
	}
	if len(sfValid.Variables) != 1 || sfValid.Variables[0].Name != "MY_VAR" || sfValid.Variables[0].Value != "valid_string" {
		t.Errorf("expected variable MY_VAR to be 'valid_string', got %+v", sfValid.Variables)
	}
	if sfValid.Variables[0].Description != "My description" {
		t.Errorf("expected description 'My description', got %q", sfValid.Variables[0].Description)
	}

	// 2. Variable with validator returning None (valid)
	sfValidValidator := loadShipfileContent(t, `
from shipyard import Builder, Variable

def check_val(v):
    if len(v) < 3:
        return "too short"
    return None

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"
    def build(self, ctx): pass

    Variables = [
        Variable("MY_VAR", "My description", "valid", validate=check_val),
    ]
`)
	if err := sfValidValidator.Validate(); err != nil {
		t.Errorf("expected valid validator to pass, got: %v", err)
	}

	// 3. Variable with validator returning error string (invalid)
	dir, err := os.MkdirTemp("", "shipfile-val-err-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sfErrPath := filepath.Join(dir, "shipfile.py")
	_ = os.WriteFile(sfErrPath, []byte(`
from shipyard import Builder, Variable

def check_val(v):
    return "bad value supplied"

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"
    def build(self, ctx): pass

    Variables = [
        Variable("MY_VAR", "My description", "test", validate=check_val),
    ]
`), 0o644)
	root, _ := filepath.Abs("../..")
	if _, err := shipfile.LoadDefault(sfErrPath, root); err == nil {
		t.Errorf("expected error when validator returns error string, got nil")
	}

	// 4. Non-string variable value must fail
	sfNonString := filepath.Join(dir, "shipfile_nonstr.py")
	_ = os.WriteFile(sfNonString, []byte(`
from shipyard import Builder, Variable

class Shipfile(Builder):
    Name = "test"
    PackageName = "test-pkg"
    Artifacts = r"^bin/app$"
    def build(self, ctx): pass

    Variables = [
        Variable("MY_VAR", "My description", 12345),
    ]
`), 0o644)
	if _, err := shipfile.LoadDefault(sfNonString, root); err == nil {
		t.Errorf("expected error when variable value is non-string, got nil")
	}

	// 5. Runtime ctx.variables dict passed during CodePatch execution
	srcDir := filepath.Join(dir, "src")
	_ = os.MkdirAll(srcDir, 0o755)
	cFile := filepath.Join(srcDir, "main.c")
	_ = os.WriteFile(cFile, []byte(`const char *banner = "DEFAULT";`), 0o644)

	interp, err := shipfile.New("")
	if err != nil {
		t.Fatal(err)
	}
	defer interp.Close()

	sfPatch, err := shipfile.ValidateString(`
from shipyard import Patcher, Variable, CodePatch

class Shipfile(Patcher):
    Name = "test"
    PackageName = "test-pkg"
    Variables = [
        Variable("BANNER", "Banner text", "initial", validate=lambda v: "too short" if len(v) < 3 else None),
    ]

    @CodePatch(r".*main\.c$")
    def apply_banner(file, ctx):
        file.replace("DEFAULT", ctx.variables["BANNER"])
`, interp)
	if err != nil {
		t.Fatalf("ValidateString failed: %v", err)
	}

	applied, err := sfPatch.RunCodePatches(srcDir, "1.0.0", io.Discard, map[string]string{"BANNER": "custom_runtime_val"})
	if err != nil {
		t.Fatalf("RunCodePatches failed: %v", err)
	}
	if len(applied) != 1 || applied[0] != "apply_banner" {
		t.Fatalf("expected apply_banner to run, got %v", applied)
	}
	patchedBytes, err := os.ReadFile(cFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(patchedBytes) != `const char *banner = "custom_runtime_val";` {
		t.Errorf("expected patched content with runtime variable, got: %s", string(patchedBytes))
	}
}

