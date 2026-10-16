package shipyard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-python/gpython/py"
	"shipyard/pkg/shipyard"
)

func newTestInterpreter(t *testing.T) *shipyard.Interpreter {
	t.Helper()
	// Find shipyard repo root to locate python package
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, "../.."))
	interp, err := shipyard.New(repoRoot)
	if err != nil {
		t.Fatalf("shipyard.New: %v", err)
	}
	t.Cleanup(func() {
		interp.Close()
	})
	return interp
}

func runPySrc(t *testing.T, interp *shipyard.Interpreter, src string) (*py.Module, error) {
	t.Helper()
	code, err := py.Compile(src+"\n", "<test>", py.ExecMode, 0, true)
	if err != nil {
		return nil, err
	}
	return py.RunCode(interp.Context(), code, "<test>", nil)
}

func TestSandboxExecutionGating(t *testing.T) {
	interp := newTestInterpreter(t)

	// 1. By default, execution should be disabled
	if interp.ExecutionEnabled() {
		t.Fatalf("expected ExecutionEnabled to be false by default")
	}

	// os.system must fail with PermissionError
	_, err := runPySrc(t, interp, "import os\nos.system('echo test')")
	if err == nil {
		t.Fatalf("expected error from os.system when execution is disabled")
	}
	if !strings.Contains(err.Error(), "execution is disabled") && !strings.Contains(err.Error(), "PermissionError") {
		t.Fatalf("expected PermissionError about execution, got: %v", err)
	}

	// shipyard.Run must fail with PermissionError
	_, err = runPySrc(t, interp, "import shipyard\nshipyard.Run('echo test')")
	if err == nil {
		t.Fatalf("expected error from shipyard.Run when execution is disabled")
	}
	if !strings.Contains(err.Error(), "build phases") && !strings.Contains(err.Error(), "PermissionError") {
		t.Fatalf("expected PermissionError about build phases, got: %v", err)
	}

	// Python global tampering (_run_enabled = True) must NOT bypass Go sandbox
	_, err = runPySrc(t, interp, "import shipyard\nshipyard._run_enabled = True\nshipyard.Run('echo test')")
	if err == nil {
		t.Fatalf("expected shipyard.Run to still fail after attempting to set _run_enabled in Python globals")
	}

	// os._exit must be blocked always in sandbox
	_, err = runPySrc(t, interp, "import os\nos._exit(0)")
	if err == nil {
		t.Fatalf("expected error from os._exit in sandbox")
	}

	// 2. Enable execution
	interp.SetExecution(true)
	if !interp.ExecutionEnabled() {
		t.Fatalf("expected ExecutionEnabled to be true")
	}

	// os.system should now work
	_, err = runPySrc(t, interp, "import os\nres = os.system('echo test')")
	if err != nil {
		t.Fatalf("expected os.system to succeed when execution is enabled, got: %v", err)
	}

	// shipyard.Run should now work
	mod, err := runPySrc(t, interp, "import shipyard\nr = shipyard.Run('echo hello')\nout = r.stdout")
	if err != nil {
		t.Fatalf("expected shipyard.Run to succeed when execution is enabled, got: %v", err)
	}
	outObj, ok := mod.Globals["out"]
	if !ok || !strings.Contains(string(outObj.(py.String)), "hello") {
		t.Fatalf("unexpected shipyard.Run output: %v", outObj)
	}

	// 3. Disable execution again
	interp.SetExecution(false)
	if interp.ExecutionEnabled() {
		t.Fatalf("expected ExecutionEnabled to be false")
	}

	_, err = runPySrc(t, interp, "import os\nos.system('echo test')")
	if err == nil {
		t.Fatalf("expected os.system to fail after disabling execution")
	}
	_, err = runPySrc(t, interp, "import shipyard\nshipyard.Run('echo test')")
	if err == nil {
		t.Fatalf("expected shipyard.Run to fail after disabling execution")
	}
}

func TestSandboxFileReadGating(t *testing.T) {
	interp := newTestInterpreter(t)

	// Create a temporary file to attempt reading
	tmpFile, err := os.CreateTemp("", "shipyard_test_*.txt")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.WriteString("secret content")
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpPath) })

	// 1. By default, file read should be disabled
	if interp.FileReadEnabled() {
		t.Fatalf("expected FileReadEnabled to be false by default")
	}

	// open() must fail
	_, err = runPySrc(t, interp, "open('"+tmpPath+"')")
	if err == nil {
		t.Fatalf("expected error from open() when file read is disabled")
	}
	if !strings.Contains(err.Error(), "file read") && !strings.Contains(err.Error(), "PermissionError") {
		t.Fatalf("expected PermissionError, got: %v", err)
	}

	// CodeFile must fail when attempting to read file
	_, err = runPySrc(t, interp, "from shipyard import CodeFile\ncf = CodeFile('"+tmpPath+"')")
	if err == nil {
		t.Fatalf("expected error from CodeFile() when file read is disabled")
	}

	// os.listdir must fail
	_, err = runPySrc(t, interp, "import os\nos.listdir('.')")
	if err == nil {
		t.Fatalf("expected error from os.listdir() when file read is disabled")
	}

	// os.getcwd must fail
	_, err = runPySrc(t, interp, "import os\nos.getcwd()")
	if err == nil {
		t.Fatalf("expected error from os.getcwd() when file read is disabled")
	}

	// glob.glob must fail
	_, err = runPySrc(t, interp, "import glob\nglob.glob('*')")
	if err == nil {
		t.Fatalf("expected error from glob.glob() when file read is disabled")
	}

	// tempfile must fail
	_, err = runPySrc(t, interp, "import tempfile\ntempfile.mkstemp()")
	if err == nil {
		t.Fatalf("expected error from tempfile.mkstemp() when file read is disabled")
	}

	// 2. Enable file read
	interp.SetFileRead(true)
	if !interp.FileReadEnabled() {
		t.Fatalf("expected FileReadEnabled to be true")
	}

	// open() should now succeed
	mod, err := runPySrc(t, interp, "f = open('"+tmpPath+"')\ncontent = f.read()\nf.close()")
	if err != nil {
		t.Fatalf("expected open() to succeed when file read is enabled, got: %v", err)
	}
	if string(mod.Globals["content"].(py.String)) != "secret content" {
		t.Fatalf("unexpected content: %v", mod.Globals["content"])
	}

	// CodeFile should now succeed
	mod, err = runPySrc(t, interp, "from shipyard import CodeFile\ncf = CodeFile('"+tmpPath+"')\ncf_content = cf.contents")
	if err != nil {
		t.Fatalf("expected CodeFile to succeed, got: %v", err)
	}
	if string(mod.Globals["cf_content"].(py.String)) != "secret content" {
		t.Fatalf("unexpected CodeFile content: %v", mod.Globals["cf_content"])
	}

	// os.listdir should now succeed
	_, err = runPySrc(t, interp, "import os\nentries = os.listdir('.')")
	if err != nil {
		t.Fatalf("expected os.listdir to succeed, got: %v", err)
	}

	// os.getcwd should now succeed
	_, err = runPySrc(t, interp, "import os\ncwd = os.getcwd()")
	if err != nil {
		t.Fatalf("expected os.getcwd to succeed, got: %v", err)
	}

	// glob.glob should now succeed
	_, err = runPySrc(t, interp, "import glob\nfiles = glob.glob('*')")
	if err != nil {
		t.Fatalf("expected glob.glob to succeed, got: %v", err)
	}

	// 3. Disable file read again
	interp.SetFileRead(false)
	if interp.FileReadEnabled() {
		t.Fatalf("expected FileReadEnabled to be false")
	}

	_, err = runPySrc(t, interp, "open('"+tmpPath+"')")
	if err == nil {
		t.Fatalf("expected open() to fail after disabling file read")
	}
}

func TestSandboxEnvPassThroughGating(t *testing.T) {
	interp := newTestInterpreter(t)

	// Set a host env var to check pass-through
	os.Setenv("SHIPYARD_SANDBOX_SECRET", "super_secret_value")
	t.Cleanup(func() {
		os.Unsetenv("SHIPYARD_SANDBOX_SECRET")
	})

	// 1. By default, EnvPassThrough should be disabled
	if interp.EnvPassThroughEnabled() {
		t.Fatalf("expected EnvPassThroughEnabled to be false by default")
	}

	// os.environ should be empty (or not have host keys)
	mod, err := runPySrc(t, interp, "import os\nhas_secret = 'SHIPYARD_SANDBOX_SECRET' in os.environ\nenv_len = len(os.environ)")
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if mod.Globals["has_secret"] != py.False {
		t.Fatalf("expected SHIPYARD_SANDBOX_SECRET to not be in os.environ")
	}
	if mod.Globals["env_len"].(py.Int) != 0 {
		t.Fatalf("expected os.environ to be empty when env pass-through is disabled, got len=%v", mod.Globals["env_len"])
	}

	// os.getenv should return default / None
	mod, err = runPySrc(t, interp, "import os\nval = os.getenv('SHIPYARD_SANDBOX_SECRET')\ndefval = os.getenv('NONEXISTENT', 'default_val')")
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if mod.Globals["val"] != py.None {
		t.Fatalf("expected os.getenv to return None, got: %v", mod.Globals["val"])
	}
	if string(mod.Globals["defval"].(py.String)) != "default_val" {
		t.Fatalf("expected default_val, got: %v", mod.Globals["defval"])
	}

	// Setting env variable via os.putenv in Python should not mutate host environment
	_, err = runPySrc(t, interp, "import os\nos.putenv('SHIPYARD_POISON', 'dangerous')")
	if err != nil {
		t.Fatalf("putenv error: %v", err)
	}
	if hostVal, exists := os.LookupEnv("SHIPYARD_POISON"); exists {
		t.Fatalf("host environment was poisoned with %q", hostVal)
	}

	// 2. Enable EnvPassThrough
	interp.SetEnvPassThrough(true)
	if !interp.EnvPassThroughEnabled() {
		t.Fatalf("expected EnvPassThroughEnabled to be true")
	}

	mod, err = runPySrc(t, interp, "import os\nhas_secret = 'SHIPYARD_SANDBOX_SECRET' in os.environ\nval = os.getenv('SHIPYARD_SANDBOX_SECRET')")
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if mod.Globals["has_secret"] != py.True {
		t.Fatalf("expected SHIPYARD_SANDBOX_SECRET to be in os.environ when enabled")
	}
	if string(mod.Globals["val"].(py.String)) != "super_secret_value" {
		t.Fatalf("expected super_secret_value, got: %v", mod.Globals["val"])
	}

	// 3. Disable EnvPassThrough again
	interp.SetEnvPassThrough(false)
	if interp.EnvPassThroughEnabled() {
		t.Fatalf("expected EnvPassThroughEnabled to be false")
	}

	mod, err = runPySrc(t, interp, "import os\nhas_secret = 'SHIPYARD_SANDBOX_SECRET' in os.environ\nval = os.getenv('SHIPYARD_SANDBOX_SECRET')")
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if mod.Globals["has_secret"] != py.False {
		t.Fatalf("expected SHIPYARD_SANDBOX_SECRET to be removed from os.environ")
	}
	if mod.Globals["val"] != py.None {
		t.Fatalf("expected os.getenv to return None after disabling")
	}
}

func TestSandboxShipfileDangerousExecution(t *testing.T) {
	interp := newTestInterpreter(t)

	// A shipfile trying to execute commands at load time must fail
	maliciousShipfile := `
import os
os.system("echo compromised > /tmp/compromised.txt")

from shipyard import Shipfile
class Shipfile:
    Name = "test"
    Url = "https://github.com/foo/bar"
`
	tmpDir := t.TempDir()
	sfPath := filepath.Join(tmpDir, "shipfile.py")
	if err := os.WriteFile(sfPath, []byte(maliciousShipfile), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := interp.RunShipfile(sfPath)
	if err == nil {
		t.Fatalf("expected RunShipfile to fail when shipfile executes commands")
	}
	if !strings.Contains(err.Error(), "PermissionError") && !strings.Contains(err.Error(), "execution is disabled") {
		t.Fatalf("expected execution disabled error, got: %v", err)
	}

	// A shipfile trying to read files at load time must fail
	maliciousReadFile := `
open("/etc/passwd")

from shipyard import Shipfile
class Shipfile:
    Name = "test"
    Url = "https://github.com/foo/bar"
`
	sfPath2 := filepath.Join(tmpDir, "shipfile2.py")
	if err := os.WriteFile(sfPath2, []byte(maliciousReadFile), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err = interp.RunShipfile(sfPath2)
	if err == nil {
		t.Fatalf("expected RunShipfile to fail when shipfile reads files")
	}
	if !strings.Contains(err.Error(), "PermissionError") && !strings.Contains(err.Error(), "file read is disabled") {
		t.Fatalf("expected file read disabled error, got: %v", err)
	}
}

func TestContextVersionAndAttributes(t *testing.T) {
	interp := newTestInterpreter(t)

	src := `
from shipyard import Context, Version

ctx = Context(version="1.2.3")
is_version = isinstance(ctx.version, Version)
version_str = str(ctx.version)
has_artifacts_dir = hasattr(ctx, "artifacts_dir")
has_env = hasattr(ctx, "env")
`
	mod, err := runPySrc(t, interp, src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	if mod.Globals["is_version"] != py.True {
		t.Fatalf("expected ctx.version to be a Version instance, got %v", mod.Globals["is_version"])
	}
	if string(mod.Globals["version_str"].(py.String)) != "1.2.3" {
		t.Fatalf("expected version_str == '1.2.3', got %v", mod.Globals["version_str"])
	}
	if mod.Globals["has_artifacts_dir"] != py.False {
		t.Fatalf("expected ctx to not have artifacts_dir attribute")
	}
	if mod.Globals["has_env"] != py.False {
		t.Fatalf("expected ctx to not have env attribute")
	}
}

func TestShipyardCodesearchBuiltin(t *testing.T) {
	interp := newTestInterpreter(t)

	// Top-level 'import codesearch' should not exist
	_, err := runPySrc(t, interp, "import codesearch")
	if err == nil {
		t.Fatalf("expected 'import codesearch' to fail (it should only be available under shipyard.codesearch)")
	}

	// 'from shipyard.codesearch import c, go, python' should work
	src := `
from shipyard.codesearch import c, go, python

c_pat = c.find_func("main")
go_pat = go.find_func("main")
py_pat = python.find_func("main")

has_c_search = hasattr(c_pat, "search")
has_go_search = hasattr(go_pat, "search")
has_py_search = hasattr(py_pat, "search")
`
	mod, err := runPySrc(t, interp, src)
	if err != nil {
		py.TracebackDump(err)
		t.Fatalf("run error: %v", err)
	}

	if mod.Globals["has_c_search"] != py.True {
		t.Fatalf("expected c.find_func to return a regex pattern")
	}
	if mod.Globals["has_go_search"] != py.True {
		t.Fatalf("expected go.find_func to return a regex pattern")
	}
	if mod.Globals["has_py_search"] != py.True {
		t.Fatalf("expected python.find_func to return a regex pattern")
	}

	// Submodule direct import should work
	src2 := `
import shipyard
has_cs = hasattr(shipyard, "codesearch")
c_mod = shipyard.codesearch.c
go_mod = shipyard.codesearch.go
py_mod = shipyard.codesearch.python

from shipyard.codesearch.c import find_func as c_find_func
from shipyard.codesearch.go import find_func as go_find_func
from shipyard.codesearch.python import find_func as py_find_func

c_define = c_mod.find_define("VERSION")
go_define = go_mod.find_define("VERSION")
py_define = py_mod.find_define("VERSION")

has_c_def = hasattr(c_define, "search")
has_go_def = hasattr(go_define, "search")
has_py_def = hasattr(py_define, "search")
`
	mod2, err := runPySrc(t, interp, src2)
	if err != nil {
		py.TracebackDump(err)
		t.Fatalf("run error: %v", err)
	}
	if mod2.Globals["has_c_def"] != py.True || mod2.Globals["has_go_def"] != py.True || mod2.Globals["has_py_def"] != py.True {
		t.Fatalf("expected direct submodule imports to work")
	}
}


