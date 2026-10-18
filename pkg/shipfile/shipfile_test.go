package shipfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shipyard/pkg/shipfile"
	shipmod "shipyard/pkg/shipfile/modules/shipyard"
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

def _check_banner(v):
    if not v.strip():
        raise ValueError("must not be empty")

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
        Variable("BANNER", "Banner greeting", "Hello world", validate=_check_banner),
        Variable("PORT", "Port number", "8080"),
    ]

    @CodePatch(r".*\.c$")
    def patch_code(self, file):
        pass

    def save(self): pass
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
    def patch_code(self, file): pass

    def save(self): pass
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
    def banner(self, file):
        b = self.ctx.variables["BANNER"]
        file.replace("options->banner = NULL;", 'options->banner = "%s";' % b)

    def save(self): pass
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
    def banner(self, file):
        val = self.ctx.variables["MISSING_KEY"]
        file.replace("a", val)

    def save(self): pass
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

func TestRunTestsSandboxExecutionAndFileWrite(t *testing.T) {
	tmpDir := t.TempDir()
	interp, err := shipfile.New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create interpreter: %v", err)
	}
	defer interp.Close()

	testFile := filepath.Join(tmpDir, "test_output.txt")
	src := `
import os
from shipyard import Builder, Tester, Run, test

class Shipfile(Builder, Tester):
    Name = "test-sandbox"
    Url = "https://github.com/example/repo"

    def build(self):
        pass

    def save(self): pass

    @test("run and file write")
    def check_run_and_write(self):
        assert os.getenv("SHIPYARD_DEPTH") in ("0", "10")
        res = Run("echo $SHIPYARD_DEPTH", quiet=True)
        assert res.stdout.strip() == "1"
        f = open("` + testFile + `", "w")
        f.write(res.stdout.strip())
        f.close()
        r = open("` + testFile + `", "r")
        data = r.read()
        r.close()
        assert data == "1"
`
	sf, err := shipfile.ValidateString(src, interp)
	if err != nil {
		t.Fatalf("ValidateString error: %v", err)
	}

	results, err := sf.RunTests(shipfile.RunContext{Version: "1.0.0"})
	if err != nil {
		t.Fatalf("RunTests error: %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("expected 1 passing test, got: %+v", results)
	}

	// Verify MaxRunDepth = 10 blocks Run() when SHIPYARD_DEPTH >= 10
	t.Setenv("SHIPYARD_DEPTH", "10")
	resultsMax, err := sf.RunTests(shipfile.RunContext{Version: "1.0.0"})
	if err != nil {
		t.Fatalf("RunTests error at max depth: %v", err)
	}
	if len(resultsMax) != 1 || resultsMax[0].Passed || !strings.Contains(resultsMax[0].Error, "maximum shipyard.Run() recursion depth (10) exceeded") {
		t.Fatalf("expected test to fail with max recursion depth error, got: %+v", resultsMax)
	}
}

// mockBuilder records all ContainerBuilder calls made during a build run.
type mockBuilder struct {
	froms    []string
	scripts  []string
	copies   [][3]string // [src, dst, fromContainer]
	writes   [][2]string // [path, content]
	workdirs []string
	saves    [][2]string // [src, dst]
	runs     []string    // display form of each argv
}

func (m *mockBuilder) From(image, name string) error {
	m.froms = append(m.froms, image)
	return nil
}
func (m *mockBuilder) Use(_ string) error { return nil }
func (m *mockBuilder) Run(argv []string, _ string) (string, string, int, error) {
	m.runs = append(m.runs, strings.Join(argv, " "))
	return "", "", 0, nil
}
func (m *mockBuilder) Copy(src string, dstAndFrom ...string) error {
	dst := "."
	from := ""
	if len(dstAndFrom) > 0 {
		dst = dstAndFrom[0]
	}
	if len(dstAndFrom) > 1 {
		from = dstAndFrom[1]
	}
	m.copies = append(m.copies, [3]string{src, dst, from})
	return nil
}
func (m *mockBuilder) WriteFile(path, content string) error {
	m.writes = append(m.writes, [2]string{path, content})
	return nil
}
func (m *mockBuilder) Workdir(path string) error {
	m.workdirs = append(m.workdirs, path)
	return nil
}
func (m *mockBuilder) Env(_, _ string) error       { return nil }
func (m *mockBuilder) User(_ string) error          { return nil }
func (m *mockBuilder) Dockerfile(_ string) error    { return nil }
func (m *mockBuilder) Save(src string, dst ...string) error {
	dstTarget := "."
	if len(dst) > 0 {
		dstTarget = dst[0]
	}
	m.saves = append(m.saves, [2]string{src, dstTarget})
	return nil
}

var _ shipmod.ContainerBuilder = (*mockBuilder)(nil)

// TestUserdBShipfileNewBuildSyntax loads the userdb shipfile from its project
// directory and verifies that RunWithBuilder drives the new Dagger-style
// container primitives (Copy, Workdir, Write, Save) instead of writing files
// to the host filesystem.
func TestUserdBShipfileNewBuildSyntax(t *testing.T) {
	sfPath := "/home/lethicon/Projects/userbd/shipfile.py"
	if _, err := os.Stat(sfPath); err != nil {
		t.Skipf("userdb shipfile not found at %s: %v", sfPath, err)
	}

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	interp, err := shipfile.New(root)
	if err != nil {
		t.Fatalf("shipfile.New: %v", err)
	}
	defer interp.Close()

	sf, err := shipfile.Load(sfPath, interp)
	if err != nil {
		t.Fatalf("load userdb shipfile: %v", err)
	}

	// Structural assertions
	if sf.Name != "userdb" {
		t.Errorf("expected Name 'userdb', got %q", sf.Name)
	}
	if !sf.IsBuild() {
		t.Error("expected IsBuild() == true")
	}
	if !sf.ImplementsTester {
		t.Error("expected ImplementsTester == true")
	}
	if len(sf.Variables) != 7 {
		t.Errorf("expected 7 variables, got %d", len(sf.Variables))
	}
	if err := sf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Exercise the new build syntax via a mock ContainerBuilder.
	mock := &mockBuilder{}
	if err := sf.RunWithBuilder(mock, shipfile.RunContext{}); err != nil {
		t.Fatalf("RunWithBuilder: %v", err)
	}

	// build() or setup() must have set base image and run commands
	foundFrom := false
	for _, f := range mock.froms {
		if f == "ubuntu:22.04" {
			foundFrom = true
			break
		}
	}
	if !foundFrom && sf.BaseImage != "ubuntu:22.04" {
		t.Errorf("expected BaseImage or From('ubuntu:22.04'), got BaseImage=%q, froms=%v", sf.BaseImage, mock.froms)
	}

	// build() must Copy the source and set Workdir
	foundCopy := false
	for _, c := range mock.copies {
		if c[0] == "userdb.c" || (c[0] == "." && c[1] == "/build") || c[0] == "src/" {
			foundCopy = true
			break
		}
	}
	if !foundCopy {
		t.Errorf("expected Copy call; copies = %v", mock.copies)
	}

	foundWorkdir := false
	for _, w := range mock.workdirs {
		if w == "/build" {
			foundWorkdir = true
			break
		}
	}
	if !foundWorkdir {
		t.Errorf("expected Workdir('/build') call; workdirs = %v", mock.workdirs)
	}

	// build() must Write vars.h into the container (not open() on host)
	foundVarsH := false
	for _, w := range mock.writes {
		if strings.Contains(w[0], "vars.h") && strings.Contains(w[1], "USERDB_USER") {
			foundVarsH = true
			break
		}
	}
	if !foundVarsH {
		t.Errorf("expected Write('*/vars.h', ...) with USERDB_USER define; writes = %v", mock.writes)
	}

	// build() must Save dist/userdb artifact
	foundSave := false
	for _, s := range mock.saves {
		if s[0] == "dist/userdb" {
			foundSave = true
			break
		}
	}
	if !foundSave {
		t.Errorf("expected Save('dist/userdb', ...); saves = %v", mock.saves)
	}
}

func TestContextVariablesDictAndResolution(t *testing.T) {
	tmpDir := t.TempDir()
	interp, err := shipfile.New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create interpreter: %v", err)
	}
	defer interp.Close()

	src := `
from shipyard import Builder, Variable

def to_int(v):
    return int(v)

class Shipfile(Builder):
    Name = "test-variables"
    PackageName = "test-variables-pkg"

    Variables = [
        Variable("PORT", "Port number", "8080", validate=to_int),
        Variable("ENV", "Target environment", "prod"),
    ]

    def build(self):
        assert isinstance(self.ctx.variables, dict), "self.ctx.variables must be a dict, got {}".format(type(self.ctx.variables))
        assert type(self.ctx.variables) is dict, "type(self.ctx.variables) must be dict"
        assert self.ctx.variables["PORT"] == 8080, "PORT must be resolved to int 8080, got {!r}".format(self.ctx.variables["PORT"])
        assert self.ctx.variables["ENV"] == "prod"
        assert self.ctx.variables.get("PORT") == 8080
        assert "PORT" in self.ctx.variables
        assert "MISSING" not in self.ctx.variables

    def save(self):
        assert isinstance(self.ctx.variables, dict)
        assert self.ctx.variables["PORT"] == 8080
`
	sf, err := shipfile.ValidateString(src, interp)
	if err != nil {
		t.Fatalf("ValidateString error: %v", err)
	}

	// 1. Run Build without overrides: default values are validated and resolved beforehand
	if err := sf.Build(shipfile.RunContext{}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// 2. Run Build with overrides: overrides are validated and resolved
	overrideSrc := `
from shipyard import Builder, Variable

def to_int(v):
    return int(v)

class Shipfile(Builder):
    Name = "test-variables-ov"
    PackageName = "test-variables-ov-pkg"

    Variables = [
        Variable("PORT", "Port number", "8080", validate=to_int),
        Variable("ENV", "Target environment", "prod"),
    ]

    def build(self):
        assert self.ctx.variables["PORT"] == 9000, "expected overridden PORT 9000, got {!r}".format(self.ctx.variables["PORT"])
        assert self.ctx.variables["ENV"] == "dev", "expected overridden ENV dev, got {!r}".format(self.ctx.variables["ENV"])

    def save(self): pass
`
	sfOv, err := shipfile.ValidateString(overrideSrc, interp)
	if err != nil {
		t.Fatalf("ValidateString error: %v", err)
	}
	rc := shipfile.RunContext{
		Variables: map[string]string{
			"PORT": "9000",
			"ENV":  "dev",
		},
	}
	if err := sfOv.Build(rc); err != nil {
		t.Fatalf("Build with overrides failed: %v", err)
	}

	// 3. Invalid override fails validation before function is executed
	rcInvalid := shipfile.RunContext{
		Variables: map[string]string{
			"PORT": "not-an-int",
		},
	}
	if err := sfOv.Build(rcInvalid); err == nil {
		t.Fatalf("expected Build to fail with invalid PORT override, got nil")
	}

	// 4. Unknown variable override fails
	rcUnknown := shipfile.RunContext{
		Variables: map[string]string{
			"NON_EXISTENT": "value",
		},
	}
	if err := sfOv.Build(rcUnknown); err == nil {
		t.Fatalf("expected Build to fail with unknown variable override, got nil")
	}

	// 5. CodePatch also receives ctx.variables as a dict with resolved values
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("ORIGINAL"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	patchSrc := `
from shipyard import Patcher, CodePatch, Variable

def to_upper(v):
    return v.upper()

class Shipfile(Patcher):
    Name = "test-patcher"
    PackageName = "test-patcher-pkg"

    Variables = [
        Variable("TAG", "Release tag", "v1.0.0", validate=to_upper),
    ]

    @CodePatch(r"test\.txt$")
    def patch_file(self, file):
        assert isinstance(self.ctx.variables, dict), "expected dict, got {}".format(type(self.ctx.variables))
        assert self.ctx.variables["TAG"] == "V2.0.0", "expected V2.0.0, got {!r}".format(self.ctx.variables["TAG"])
        file.replace("ORIGINAL", self.ctx.variables["TAG"])

    def save(self): pass
`
	sfPatch, err := shipfile.ValidateString(patchSrc, interp)
	if err != nil {
		t.Fatalf("ValidateString error: %v", err)
	}
	sfPatch.Dir = tmpDir
	applied, err := sfPatch.RunCodePatches(tmpDir, "1.0", os.Stderr, map[string]string{"TAG": "v2.0.0"})
	if err != nil {
		t.Fatalf("RunCodePatches error: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 patch applied, got %d", len(applied))
	}
	data, _ := os.ReadFile(testFile)
	if string(data) != "V2.0.0" {
		t.Errorf("expected patched content 'V2.0.0', got %q", string(data))
	}
}
