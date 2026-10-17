package shipfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shipyard/pkg/shipfile"
)

func TestLoadOpensshShipfile(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	sfPath := filepath.Join(root, "examples/openssh/shipfile.py")
	sf, err := shipfile.LoadDefault(sfPath, root)
	if err != nil {
		t.Fatalf("failed to load shipfile: %v", err)
	}

	if sf.Name != "openssh" {
		t.Errorf("expected Name 'openssh', got %q", sf.Name)
	}
	if sf.Url != "https://github.com/openssh/openssh-portable" {
		t.Errorf("expected Url 'https://github.com/openssh/openssh-portable', got %q", sf.Url)
	}
	if sf.PackageName != "openssh" {
		t.Errorf("expected PackageName 'openssh', got %q", sf.PackageName)
	}
	if len(sf.Variables) != 1 || sf.Variables[0].Name != "__BANNER__" || sf.Variables[0].Value != "opensource is good" {
		t.Errorf("expected __BANNER__ variable 'opensource is good', got %+v", sf.Variables)
	}

	if !sf.IsPatch() {
		t.Errorf("expected sf.IsPatch() == true for openssh (subclasses Patcher)")
	}
	if sf.IsBuild() {
		t.Errorf("expected sf.IsBuild() == false for openssh (does not subclass Builder)")
	}
	if !sf.HasCodePatches {
		t.Errorf("expected sf.HasCodePatches == true for openssh")
	}
	if err := sf.Validate(); err != nil {
		t.Errorf("expected openssh shipfile to validate, got error: %v", err)
	}
}

func TestLoadHelloShipfile(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	sfPath := filepath.Join(root, "examples/hello/shipfile.py")
	sf, err := shipfile.LoadDefault(sfPath, root)
	if err != nil {
		t.Fatalf("failed to load shipfile: %v", err)
	}

	if sf.Name != "hello" {
		t.Errorf("expected Name 'hello', got %q", sf.Name)
	}
	if !sf.IsBuild() {
		t.Errorf("expected sf.IsBuild() == true for hello (subclasses Builder)")
	}
	if sf.IsPatch() {
		t.Errorf("expected sf.IsPatch() == false for hello (does not subclass Patcher)")
	}
	if err := sf.Validate(); err != nil {
		t.Errorf("expected hello shipfile to validate, got error: %v", err)
	}
}

func TestLoadStringAndDependencies(t *testing.T) {
	interp, err := shipfile.New("")
	if err != nil {
		t.Fatalf("shipfile.New: %v", err)
	}
	defer interp.Close()

	src := `from shipyard import Patcher, CodePatch, Variable, Dep, Dependency

class Shipfile(Patcher):
    Name = "test-tool"
    PackageName = "test-pkg"

    Requires = [
        "curl >= 7.0",
        "bash",
        Dep("openssl", ">=", "1.1.0"),
        Dependency({"debian": "libssl-dev", "rhel": "openssl-devel"}, ">=", "1.1.0"),
    ]

    Variables = [
        Variable("BANNER", "Banner greeting", "Hello world", validate=lambda v: "must not be empty" if not v.strip() else None),
        Variable("PORT", "Port number", "8080"),
    ]

    @CodePatch(r".*\.c$")
    def patch_code(file, ctx):
        pass
`
	sf, err := shipfile.ValidateString(src, interp)
	if err != nil {
		t.Fatalf("ValidateString error: %v", err)
	}

	if sf.Name != "test-tool" {
		t.Errorf("expected Name 'test-tool', got %q", sf.Name)
	}
	if !sf.IsPatch() {
		t.Errorf("expected sf.IsPatch() == true")
	}

	// Variables
	if len(sf.Variables) != 2 {
		t.Fatalf("expected 2 variables, got %d", len(sf.Variables))
	}
	if sf.Variables[0].Name != "BANNER" || sf.Variables[0].Value != "Hello world" || sf.Variables[0].Description != "Banner greeting" {
		t.Errorf("unexpected variable 0: %+v", sf.Variables[0])
	}
	if sf.Variables[1].Name != "PORT" || sf.Variables[1].Value != "8080" || sf.Variables[1].Description != "Port number" {
		t.Errorf("unexpected variable 1: %+v", sf.Variables[1])
	}

	// ValidateVariable
	if err := sf.ValidateVariable("BANNER", "custom banner"); err != nil {
		t.Errorf("expected valid for 'custom banner', got: %v", err)
	}
	if err := sf.ValidateVariable("BANNER", "   "); err == nil {
		t.Errorf("expected error for empty banner, got nil")
	}

	// Dependencies
	if len(sf.Dependencies) != 4 {
		t.Fatalf("expected 4 dependencies, got %d", len(sf.Dependencies))
	}

	// 1. "curl >= 7.0"
	d0 := sf.Dependencies[0]
	if d0.Names[""] != "curl" || d0.Op != ">=" || string(d0.Version) != "7.0" {
		t.Errorf("unexpected dep 0: %+v", d0)
	}

	// 2. "bash"
	d1 := sf.Dependencies[1]
	if d1.Names[""] != "bash" || d1.Op != "" || string(d1.Version) != "" {
		t.Errorf("unexpected dep 1: %+v", d1)
	}

	// 3. Dep("openssl", ">=", "1.1.0")
	d2 := sf.Dependencies[2]
	if d2.Names[""] != "openssl" || d2.Op != ">=" || string(d2.Version) != "1.1.0" {
		t.Errorf("unexpected dep 2: %+v", d2)
	}

	// 4. Dependency({"debian": "libssl-dev", "rhel": "openssl-devel"}, ">=", "1.1.0")
	d3 := sf.Dependencies[3]
	if d3.Names["debian"] != "libssl-dev" || d3.Names["rhel"] != "openssl-devel" || d3.Op != ">=" || string(d3.Version) != "1.1.0" {
		t.Errorf("unexpected dep 3: %+v", d3)
	}
}

func TestGetVariables(t *testing.T) {
	interp, err := shipfile.New("")
	if err != nil {
		t.Fatalf("failed to create interpreter: %v", err)
	}
	defer interp.Close()

	src := `
from shipyard import Patcher, Variable, CodePatch

class Shipfile(Patcher):
    Name = "test-pkg"
    PackageName = "test-pkg"

    Variables = [
        Variable("BANNER", "My greeting banner", "welcome"),
        Variable("PORT", "Port configuration", "9000"),
    ]

    @CodePatch(r".*\.c$")
    def patch_code(file, ctx): pass
`
	sf, err := shipfile.ValidateString(src, interp)
	if err != nil {
		t.Fatalf("ValidateString error: %v", err)
	}

	vars := sf.GetVariables()
	if len(vars) != 2 {
		t.Fatalf("expected 2 variables from GetVariables(), got %d", len(vars))
	}

	varMap := make(map[string]shipfile.VariableDef)
	for _, v := range vars {
		varMap[v.Name] = v
	}

	banner, ok := varMap["BANNER"]
	if !ok {
		t.Fatalf("expected BANNER in GetVariables() results")
	}
	if banner.Value != "welcome" {
		t.Errorf("expected BANNER value 'welcome', got %q", banner.Value)
	}
	if banner.Description != "My greeting banner" {
		t.Errorf("expected BANNER description 'My greeting banner', got %q", banner.Description)
	}

	port, ok := varMap["PORT"]
	if !ok {
		t.Fatalf("expected PORT in GetVariables() results")
	}
	if port.Value != "9000" {
		t.Errorf("expected PORT value '9000', got %q", port.Value)
	}
	if port.Description != "Port configuration" {
		t.Errorf("expected PORT description 'Port configuration', got %q", port.Description)
	}

	// Test GetVariable
	vBanner, found := sf.GetVariable("BANNER")
	if !found {
		t.Errorf("expected GetVariable('BANNER') to find variable")
	}
	if vBanner.Value != "welcome" {
		t.Errorf("expected GetVariable('BANNER') value 'welcome', got %q", vBanner.Value)
	}

	_, foundMissing := sf.GetVariable("NON_EXISTENT")
	if foundMissing {
		t.Errorf("expected GetVariable('NON_EXISTENT') to return false")
	}
}

func TestRunCodePatchesSignatureAndErrors(t *testing.T) {
	tmpDir := t.TempDir()
	interp, err := shipfile.New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create interpreter: %v", err)
	}
	defer interp.Close()

	// Write a sample servconf.c
	cFile := filepath.Join(tmpDir, "servconf.c")
	if err := os.WriteFile(cFile, []byte("options->banner = NULL;\n"), 0644); err != nil {
		t.Fatalf("failed to write servconf.c: %v", err)
	}

	src := `
from shipyard import Patcher, Variable, CodePatch

class Shipfile(Patcher):
    Name = "openssh-test"
    PackageName = "openssh-server"

    Variables = [
        Variable("BANNER", "Custom banner", "TEST_BANNER"),
    ]

    @CodePatch(r"servconf\.c$")
    def banner(file, ctx):
        b = ctx.variables["BANNER"]
        file.replace("options->banner = NULL;", 'options->banner = "%s";' % b)
`
	sf, err := shipfile.ValidateString(src, interp)
	if err != nil {
		t.Fatalf("ValidateString error: %v", err)
	}
	sf.Dir = tmpDir

	applied, err := sf.RunCodePatches(tmpDir, "9.0p1", os.Stderr, map[string]string{"BANNER": "CUSTOM_BANNER"})
	if err != nil {
		t.Fatalf("RunCodePatches failed: %v", err)
	}
	if len(applied) != 1 || applied[0] != "banner" {
		t.Fatalf("expected 1 applied patch 'banner', got %+v", applied)
	}

	data, err := os.ReadFile(cFile)
	if err != nil {
		t.Fatalf("failed to read patched servconf.c: %v", err)
	}
	if string(data) != "options->banner = \"CUSTOM_BANNER\";\n" {
		t.Errorf("unexpected patched content: %q", string(data))
	}

	// Also verify that if a CodePatch function raises an exception, we get the real exception
	// and NOT AttributeError: "'Shipfile' has no attribute 'replace'"
	errSrc := `
from shipyard import Patcher, CodePatch

class Shipfile(Patcher):
    Name = "err-test"
    PackageName = "err-pkg"

    @CodePatch(r"servconf\.c$")
    def banner(file, ctx):
        val = ctx.variables["MISSING_KEY"]
        file.replace("a", val)
`
	sfErr, err := shipfile.ValidateString(errSrc, interp)
	if err != nil {
		t.Fatalf("ValidateString error: %v", err)
	}
	sfErr.Dir = tmpDir

	_, err = sfErr.RunCodePatches(tmpDir, "9.0p1", os.Stderr, nil)
	if err == nil {
		t.Fatalf("expected RunCodePatches to return an error when KeyError is raised")
	}
	if strings.Contains(err.Error(), "has no attribute 'replace'") {
		t.Fatalf("RunCodePatches masked original error with Shipfile fallback error: %v", err)
	}
	if !strings.Contains(err.Error(), "MISSING_KEY") {
		t.Errorf("expected KeyError for MISSING_KEY in error message, got: %v", err)
	}

	// Test examples/openssh/shipfile.py against mock servconf.c with default variable
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	opensshSf, err := shipfile.LoadDefault(filepath.Join(root, "examples/openssh/shipfile.py"), root)
	if err != nil {
		t.Fatalf("LoadDefault openssh failed: %v", err)
	}
	unpatchedServconf := "void fill_default_options(ServerOptions *options) {\n\tif (options->version_addendum == NULL)\n\t\toptions->version_addendum = xstrdup(\"\");\n}\n"
	if err := os.WriteFile(cFile, []byte(unpatchedServconf), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := opensshSf.RunCodePatches(tmpDir, "9.9p1", os.Stderr); err != nil {
		t.Fatalf("openssh RunCodePatches failed: %v", err)
	}
	patched, err := os.ReadFile(cFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patched), `options->version_addendum = xstrdup("opensource is good");`) {
		t.Errorf("expected openssh banner patch to substitute default __BANNER__, got: %s", string(patched))
	}
}



