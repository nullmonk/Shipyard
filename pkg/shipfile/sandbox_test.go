package shipfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-python/gpython/py"
	"shipyard/pkg/shipfile"
)

func newTestInterpreter(t *testing.T) *shipfile.Interpreter {
	t.Helper()
	// Find shipyard repo root to locate python package
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, "../.."))
	interp, err := shipfile.New(repoRoot)
	if err != nil {
		t.Fatalf("shipfile.New: %v", err)
	}
	t.Cleanup(func() {
		interp.Close()
	})
	return interp
}

func runPySrc(t *testing.T, interp *shipfile.Interpreter, src string) (*py.Module, error) {
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
	if interp.Sandbox().ExecutionEnabled() {
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
	interp.Sandbox().SetExecution(true)
	if !interp.Sandbox().ExecutionEnabled() {
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
	interp.Sandbox().SetExecution(false)
	if interp.Sandbox().ExecutionEnabled() {
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
	if interp.Sandbox().FileReadEnabled() {
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
	interp.Sandbox().SetFileRead(true)
	if !interp.Sandbox().FileReadEnabled() {
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
	interp.Sandbox().SetFileRead(false)
	if interp.Sandbox().FileReadEnabled() {
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
	if interp.Sandbox().EnvPassThroughEnabled() {
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
	interp.Sandbox().SetEnvPassThrough(true)
	if !interp.Sandbox().EnvPassThroughEnabled() {
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
	interp.Sandbox().SetEnvPassThrough(false)
	if interp.Sandbox().EnvPassThroughEnabled() {
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

func TestOSPathModule(t *testing.T) {
	interp := newTestInterpreter(t)

	// Pure path manipulations must work even when file read is disabled
	src := `
import os
import os.path
import posixpath

j1 = os.path.join("a", "b", "c")
j2 = os.path.join("/a", "b/")
d1 = os.path.dirname("/path/to/file.txt")
b1 = os.path.basename("/path/to/file.txt")
head, tail = os.path.split("/path/to/file.txt")
root, ext = os.path.splitext("/path/to/file.tar.gz")
norm = os.path.normpath("/a/b/../c/./d")
is_abs1 = os.path.isabs("/a/b")
is_abs2 = os.path.isabs("a/b")
posix_j = posixpath.join("x", "y")
`
	mod, err := runPySrc(t, interp, src)
	if err != nil {
		t.Fatalf("run pure path manipulation error: %v", err)
	}

	if mod.Globals["j1"] != py.String("a/b/c") {
		t.Fatalf("expected 'a/b/c', got %v", mod.Globals["j1"])
	}
	if mod.Globals["j2"] != py.String("/a/b/") {
		t.Fatalf("expected '/a/b/', got %v", mod.Globals["j2"])
	}
	if mod.Globals["d1"] != py.String("/path/to") {
		t.Fatalf("expected '/path/to', got %v", mod.Globals["d1"])
	}
	if mod.Globals["b1"] != py.String("file.txt") {
		t.Fatalf("expected 'file.txt', got %v", mod.Globals["b1"])
	}
	if mod.Globals["head"] != py.String("/path/to") || mod.Globals["tail"] != py.String("file.txt") {
		t.Fatalf("unexpected split result: %v, %v", mod.Globals["head"], mod.Globals["tail"])
	}
	if mod.Globals["root"] != py.String("/path/to/file.tar") || mod.Globals["ext"] != py.String(".gz") {
		t.Fatalf("unexpected splitext result: %v, %v", mod.Globals["root"], mod.Globals["ext"])
	}
	if mod.Globals["norm"] != py.String("/a/c/d") {
		t.Fatalf("expected '/a/c/d', got %v", mod.Globals["norm"])
	}
	if mod.Globals["is_abs1"] != py.True || mod.Globals["is_abs2"] != py.False {
		t.Fatalf("unexpected isabs results: %v, %v", mod.Globals["is_abs1"], mod.Globals["is_abs2"])
	}
	if mod.Globals["posix_j"] != py.String("x/y") {
		t.Fatalf("expected 'x/y', got %v", mod.Globals["posix_j"])
	}

	// File read operations (exists, isfile, isdir, getsize) must fail when file read is disabled
	testFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	_, err = runPySrc(t, interp, `import os.path; os.path.exists('`+testFile+`')`)
	if err == nil || !strings.Contains(err.Error(), "PermissionError") {
		t.Fatalf("expected PermissionError for os.path.exists when file read is disabled, got: %v", err)
	}

	_, err = runPySrc(t, interp, `import os.path; os.path.isfile('`+testFile+`')`)
	if err == nil || !strings.Contains(err.Error(), "PermissionError") {
		t.Fatalf("expected PermissionError for os.path.isfile when file read is disabled, got: %v", err)
	}

	// When file read is enabled, they must succeed
	interp.Sandbox().SetFileRead(true)
	checkSrc := `
import os.path
ex = os.path.exists('` + testFile + `')
is_f = os.path.isfile('` + testFile + `')
is_d = os.path.isdir('` + filepath.Dir(testFile) + `')
sz = os.path.getsize('` + testFile + `')
`
	checkMod, err := runPySrc(t, interp, checkSrc)
	if err != nil {
		t.Fatalf("run file read check error: %v", err)
	}
	if checkMod.Globals["ex"] != py.True {
		t.Fatalf("expected exists to be True")
	}
	if checkMod.Globals["is_f"] != py.True {
		t.Fatalf("expected isfile to be True")
	}
	if checkMod.Globals["is_d"] != py.True {
		t.Fatalf("expected isdir to be True")
	}
	if checkMod.Globals["sz"] != py.Int(11) {
		t.Fatalf("expected getsize 11, got %v", checkMod.Globals["sz"])
	}
}

func TestOSStatAndWalk(t *testing.T) {
	interp := newTestInterpreter(t)

	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	testFile := filepath.Join(subDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("test content 123"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// 1. By default file read is disabled: os.stat and os.walk must fail
	_, err := runPySrc(t, interp, `import os; os.stat('`+testFile+`')`)
	if err == nil || !strings.Contains(err.Error(), "PermissionError") {
		t.Fatalf("expected PermissionError for os.stat when file read is disabled, got: %v", err)
	}

	_, err = runPySrc(t, interp, `import os; list(os.walk('`+tmpDir+`'))`)
	if err == nil || !strings.Contains(err.Error(), "PermissionError") {
		t.Fatalf("expected PermissionError for os.walk when file read is disabled, got: %v", err)
	}

	// 2. Enable file read: os.stat and os.walk must succeed
	interp.Sandbox().SetFileRead(true)

	statSrc := `
import os
st = os.stat('` + testFile + `')
size_attr = st.st_size
size_idx = st[6]
st_len = len(st)
has_mode = hasattr(st, "st_mode")
has_mtime = hasattr(st, "st_mtime")
`
	statMod, err := runPySrc(t, interp, statSrc)
	if err != nil {
		t.Fatalf("os.stat run error: %v", err)
	}

	expectedLen := int64(len("test content 123"))
	if statMod.Globals["size_attr"] != py.Int(expectedLen) {
		t.Fatalf("expected st_size %d, got %v", expectedLen, statMod.Globals["size_attr"])
	}
	if statMod.Globals["size_idx"] != py.Int(expectedLen) {
		t.Fatalf("expected st[6] %d, got %v", expectedLen, statMod.Globals["size_idx"])
	}
	if statMod.Globals["st_len"] != py.Int(10) {
		t.Fatalf("expected len(st) 10, got %v", statMod.Globals["st_len"])
	}
	if statMod.Globals["has_mode"] != py.True || statMod.Globals["has_mtime"] != py.True {
		t.Fatalf("expected stat to have st_mode and st_mtime")
	}

	// Test os.walk
	walkSrc := `
import os
walk_result = []
for root, dirs, files in os.walk('` + tmpDir + `'):
    walk_result.append((root, list(dirs), list(files)))
`
	walkMod, err := runPySrc(t, interp, walkSrc)
	if err != nil {
		t.Fatalf("os.walk run error: %v", err)
	}

	wRes, ok := walkMod.Globals["walk_result"].(*py.List)
	if !ok || len(wRes.Items) != 2 {
		t.Fatalf("expected 2 directories walked, got %v", walkMod.Globals["walk_result"])
	}
}

func TestJSONModule(t *testing.T) {
	interp := newTestInterpreter(t)

	jsonSrc := `
import json

data = {
    "name": "shipyard",
    "version": 1,
    "pi": 3.14,
    "active": True,
    "tags": ["ci", "cd"],
    "meta": None
}

dumped = json.dumps(data)
loaded = json.loads(dumped)

name_ok = loaded["name"] == "shipyard"
ver_ok = loaded["version"] == 1
tags_len = len(loaded["tags"])
first_tag = loaded["tags"][0]
meta_is_none = loaded["meta"] is None
active_ok = loaded["active"] is True
`
	mod, err := runPySrc(t, interp, jsonSrc)
	if err != nil {
		t.Fatalf("json test error: %v", err)
	}

	if mod.Globals["name_ok"] != py.True {
		t.Fatalf("expected name_ok True")
	}
	if mod.Globals["ver_ok"] != py.True {
		t.Fatalf("expected ver_ok True")
	}
	if mod.Globals["tags_len"] != py.Int(2) {
		t.Fatalf("expected tags_len 2, got %v", mod.Globals["tags_len"])
	}
	if mod.Globals["first_tag"] != py.String("ci") {
		t.Fatalf("expected first_tag 'ci', got %v", mod.Globals["first_tag"])
	}
	if mod.Globals["meta_is_none"] != py.True {
		t.Fatalf("expected meta_is_none True")
	}
	if mod.Globals["active_ok"] != py.True {
		t.Fatalf("expected active_ok True")
	}
}

func TestHashlibModule(t *testing.T) {
	interp := newTestInterpreter(t)

	hashSrc := `
import hashlib

# sha256
h256 = hashlib.sha256(b"hello world")
h256_hex = h256.hexdigest()

# incremental update
h_inc = hashlib.sha256()
h_inc.update(b"hello ")
h_inc.update(b"world")
h_inc_hex = h_inc.hexdigest()

# sha512
h512 = hashlib.sha512(b"hello world")
h512_hex = h512.hexdigest()

# md5
hmd5 = hashlib.md5(b"hello world")
hmd5_hex = hmd5.hexdigest()

# sha1
hsha1 = hashlib.sha1(b"hello world")
hsha1_hex = hsha1.hexdigest()

# new constructor
hnew = hashlib.new("sha256", b"hello world")
hnew_hex = hnew.hexdigest()
`
	mod, err := runPySrc(t, interp, hashSrc)
	if err != nil {
		t.Fatalf("hashlib test error: %v", err)
	}

	expectedSHA256 := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if mod.Globals["h256_hex"] != py.String(expectedSHA256) {
		t.Fatalf("expected sha256 %s, got %v", expectedSHA256, mod.Globals["h256_hex"])
	}
	if mod.Globals["h_inc_hex"] != py.String(expectedSHA256) {
		t.Fatalf("expected incremental sha256 %s, got %v", expectedSHA256, mod.Globals["h_inc_hex"])
	}
	if mod.Globals["hnew_hex"] != py.String(expectedSHA256) {
		t.Fatalf("expected new sha256 %s, got %v", expectedSHA256, mod.Globals["hnew_hex"])
	}

	expectedMD5 := "5eb63bbbe01eeed093cb22bb8f5acdc3"
	if mod.Globals["hmd5_hex"] != py.String(expectedMD5) {
		t.Fatalf("expected md5 %s, got %v", expectedMD5, mod.Globals["hmd5_hex"])
	}

	expectedSHA1 := "2aae6c35c94fcfb415dbe95f408b9ce91ee846ed"
	if mod.Globals["hsha1_hex"] != py.String(expectedSHA1) {
		t.Fatalf("expected sha1 %s, got %v", expectedSHA1, mod.Globals["hsha1_hex"])
	}

	expectedSHA512 := "309ecc489c12d6eb4cc40f50c902f2b4d0ed77ee511a7c7a9bcd3ca86d4cd86f989dd35bc5ff499670da34255b45b0cfd830e81f605dcf7dc5542e93ae9cd76f"
	if mod.Globals["h512_hex"] != py.String(expectedSHA512) {
		t.Fatalf("expected sha512 %s, got %v", expectedSHA512, mod.Globals["h512_hex"])
	}
}

func TestVariableObject(t *testing.T) {
	interp := newTestInterpreter(t)

	// 1. Valid string variable
	validSrc := `
from shipyard import Variable

v = Variable("BANNER", "A banner comment", "opensource is good")
name_ok = v.name == "BANNER"
desc_ok = v.description == "A banner comment"
val_ok = v.value == "opensource is good"
`
	mod, err := runPySrc(t, interp, validSrc)
	if err != nil {
		t.Fatalf("expected valid variable to succeed, got: %v", err)
	}
	if mod.Globals["name_ok"] != py.True || mod.Globals["desc_ok"] != py.True || mod.Globals["val_ok"] != py.True {
		t.Fatalf("unexpected variable attributes: %v", mod.Globals)
	}

	// 2. Variable with non-string value must fail
	nonStringSrc := `
from shipyard import Variable

v = Variable("PORT", "Number port", 8080)
`
	if _, err := runPySrc(t, interp, nonStringSrc); err == nil {
		t.Fatalf("expected variable with int value to fail, got nil")
	}

	// 3. Validator returning None (valid)
	validFnSrc := `
from shipyard import Variable

def check_banner(val):
    if len(val) < 3:
        return "too short"
    return None

v = Variable("BANNER", "Banner comment", "hello world", validate=check_banner)
`
	if _, err := runPySrc(t, interp, validFnSrc); err != nil {
		t.Fatalf("expected valid validator to succeed, got: %v", err)
	}

	// 4. Validator raising an exception (invalid)
	invalidFnSrc := `
from shipyard import Variable

def check_banner(val):
    if len(val) < 10:
        raise ValueError("too short, must be at least 10 chars")

v = Variable("BANNER", "Banner comment", "short", validate=check_banner)
`
	if _, err := runPySrc(t, interp, invalidFnSrc); err == nil {
		t.Fatalf("expected validator that raises to fail, got nil")
	}

	// 5. Non-string description must fail
	badDescSrc := `
from shipyard import Variable

v = Variable("FOO", 12345, "bar")
`
	if _, err := runPySrc(t, interp, badDescSrc); err == nil {
		t.Fatalf("expected non-string description to fail, got nil")
	}

	// 6. Validator returning a non-string type is accepted at construction time
	anyTypeSrc := `
from shipyard import Variable

def to_int(val):
    return int(val)

v = Variable("PORT", "Port number", "8080", validate=to_int)
`
	if _, err := runPySrc(t, interp, anyTypeSrc); err != nil {
		t.Fatalf("expected validator returning int to succeed at construction, got: %v", err)
	}
}

func TestShipfileGetVariablesPython(t *testing.T) {
	interp := newTestInterpreter(t)

	src := `
import shipyard
from shipyard import Shipfile, Variable

class MyShipfile(Shipfile):
    Variables = [
        Variable("VAR_A", "First variable", "alpha"),
        Variable("VAR_B", "Second variable", "beta"),
    ]

inst = MyShipfile()
inst_vars = inst.get_variables()
mod_vars = shipyard.get_variables(MyShipfile)
`
	mod, err := runPySrc(t, interp, src)
	if err != nil {
		t.Fatalf("failed to run python get_variables: %v", err)
	}

	instVarsObj := mod.Globals["inst_vars"]
	modVarsObj := mod.Globals["mod_vars"]

	checkList := func(obj py.Object, label string) {
		lst, ok := obj.(*py.List)
		if !ok {
			t.Fatalf("%s: expected *py.List, got %T", label, obj)
		}
		if len(lst.Items) != 2 {
			t.Fatalf("%s: expected 2 variables, got %d", label, len(lst.Items))
		}
		found := make(map[string]map[string]string)
		for _, itm := range lst.Items {
			dict, err := py.DictCheck(itm)
			if err != nil {
				t.Fatalf("%s: expected dict item, got error: %v", label, err)
			}
			name := string(dict["name"].(py.String))
			found[name] = map[string]string{
				"description": string(dict["description"].(py.String)),
				"value":       string(dict["value"].(py.String)),
			}
		}
		if found["VAR_A"]["value"] != "alpha" || found["VAR_A"]["description"] != "First variable" {
			t.Errorf("%s: unexpected VAR_A: %+v", label, found["VAR_A"])
		}
		if found["VAR_B"]["value"] != "beta" || found["VAR_B"]["description"] != "Second variable" {
			t.Errorf("%s: unexpected VAR_B: %+v", label, found["VAR_B"])
		}
	}

	checkList(instVarsObj, "inst_vars")
	checkList(modVarsObj, "mod_vars")
}

func TestRunAndScriptStreaming(t *testing.T) {
	interp := newTestInterpreter(t)
	interp.Sandbox().SetExecution(true)

	// Test 1: Run streams output to os.Stdout when quiet=False (default)
	origStdout := os.Stdout
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wOut

	srcRun := `
import shipyard
res = shipyard.Run("echo streamed_run_output")
stdout_val = res.stdout
rc_val = res.returncode
`
	mod, err := runPySrc(t, interp, srcRun)
	wOut.Close()
	os.Stdout = origStdout

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	buf := make([]byte, 1024)
	n, _ := rOut.Read(buf)
	streamed := string(buf[:n])
	if !strings.Contains(streamed, "streamed_run_output") {
		t.Errorf("expected stdout to stream 'streamed_run_output', got %q", streamed)
	}

	outVal := string(mod.Globals["stdout_val"].(py.String))
	if !strings.Contains(outVal, "streamed_run_output") {
		t.Errorf("expected res.stdout to contain 'streamed_run_output', got %q", outVal)
	}
	rcVal := int(mod.Globals["rc_val"].(py.Int))
	if rcVal != 0 {
		t.Errorf("expected returncode 0, got %d", rcVal)
	}

	// Test 2: Run does not stream when quiet=True
	origStdout = os.Stdout
	rOut, wOut, _ = os.Pipe()
	os.Stdout = wOut

	srcQuiet := `
import shipyard
res_q = shipyard.Run("echo quiet_run_output", quiet=True)
stdout_q = res_q.stdout
`
	modQ, err := runPySrc(t, interp, srcQuiet)
	wOut.Close()
	os.Stdout = origStdout

	if err != nil {
		t.Fatalf("Run quiet failed: %v", err)
	}
	n, _ = rOut.Read(buf)
	streamedQ := string(buf[:n])
	if strings.Contains(streamedQ, "quiet_run_output") {
		t.Errorf("expected quiet Run not to stream output, but got %q", streamedQ)
	}
	outValQ := string(modQ.Globals["stdout_q"].(py.String))
	if !strings.Contains(outValQ, "quiet_run_output") {
		t.Errorf("expected res_q.stdout to contain 'quiet_run_output', got %q", outValQ)
	}

	// Test 3: Script without shebang (defaults to /bin/sh) streams output
	origStdout = os.Stdout
	rOut, wOut, _ = os.Pipe()
	os.Stdout = wOut

	srcScript := `
from shipyard import Script
s = Script("""
echo streamed_script_output
""")
s_out = s.stdout
s_rc = s.returncode
s_bool = True if s else False
`
	modS, err := runPySrc(t, interp, srcScript)
	wOut.Close()
	os.Stdout = origStdout

	if err != nil {
		t.Fatalf("Script failed: %v", err)
	}
	n, _ = rOut.Read(buf)
	streamedS := string(buf[:n])
	if !strings.Contains(streamedS, "streamed_script_output") {
		t.Errorf("expected stdout to stream 'streamed_script_output', got %q", streamedS)
	}
	sOutVal := string(modS.Globals["s_out"].(py.String))
	if !strings.Contains(sOutVal, "streamed_script_output") {
		t.Errorf("expected s.stdout to contain 'streamed_script_output', got %q", sOutVal)
	}
	sRc := int(modS.Globals["s_rc"].(py.Int))
	if sRc != 0 {
		t.Errorf("expected s.returncode 0, got %d", sRc)
	}
	sBool := modS.Globals["s_bool"]
	if sBool != py.True {
		t.Errorf("expected bool(s) to be True, got %v", sBool)
	}

	// Test 4: Script with shebang
	origStdout = os.Stdout
	rOut, wOut, _ = os.Pipe()
	os.Stdout = wOut

	srcScriptShebang := `
from shipyard import Script
s = Script("""#!/bin/bash
echo from_shebang
""")
s_out = s.stdout
s_rc = s.returncode
`
	modShebang, err := runPySrc(t, interp, srcScriptShebang)
	wOut.Close()
	os.Stdout = origStdout

	if err != nil {
		t.Fatalf("Script with shebang failed: %v", err)
	}
	n, _ = rOut.Read(buf)
	streamedShebang := string(buf[:n])
	if strings.Contains(streamedShebang, "$ /bin/bash\n") {
		t.Errorf("expected stream NOT to contain '$ /bin/bash\\n', got %q", streamedShebang)
	}
	if !strings.Contains(streamedShebang, "echo from_shebang") || !strings.Contains(streamedShebang, "lines in script") {
		t.Errorf("expected stream to contain 'echo from_shebang' and 'lines in script', got %q", streamedShebang)
	}
	shebangOut := string(modShebang.Globals["s_out"].(py.String))
	if !strings.Contains(shebangOut, "from_shebang") {
		t.Errorf("expected shebang script stdout to contain 'from_shebang', got %q", shebangOut)
	}
}

