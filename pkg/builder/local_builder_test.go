package builder

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalBuilderRunStreaming(t *testing.T) {
	var outBuf, cmdOutBuf, cmdErrBuf bytes.Buffer

	lb := NewLocalBuilder(t.TempDir(), true)
	lb.out = &outBuf
	lb.cmdOut = &cmdOutBuf
	lb.cmdErr = &cmdErrBuf

	// Test 1: Run command streams to cmdOut and prints command to out
	stdout, stderr, rc, err := lb.Run([]string{"/bin/sh", "-c", "echo hello_local"}, "")
	if err != nil {
		t.Fatalf("LocalBuilder.Run failed: %v", err)
	}
	if rc != 0 {
		t.Errorf("expected rc 0, got %d", rc)
	}
	if !strings.Contains(stdout, "hello_local") {
		t.Errorf("expected stdout 'hello_local', got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr, got %q", stderr)
	}
	if !strings.Contains(cmdOutBuf.String(), "hello_local") {
		t.Errorf("expected cmdOut to stream 'hello_local', got %q", cmdOutBuf.String())
	}
	if !strings.Contains(outBuf.String(), "echo hello_local") {
		t.Errorf("expected out to show command, got %q", outBuf.String())
	}

	// Test 2: Run script via stdin
	cmdOutBuf.Reset()
	scriptStdout, _, scriptRc, err := lb.Run([]string{"/bin/sh"}, "echo from_script_stdin")
	if err != nil {
		t.Fatalf("LocalBuilder.Run with stdin failed: %v", err)
	}
	if scriptRc != 0 {
		t.Errorf("expected scriptRc 0, got %d", scriptRc)
	}
	if !strings.Contains(scriptStdout, "from_script_stdin") {
		t.Errorf("expected scriptStdout 'from_script_stdin', got %q", scriptStdout)
	}
	if !strings.Contains(cmdOutBuf.String(), "from_script_stdin") {
		t.Errorf("expected cmdOut to stream 'from_script_stdin', got %q", cmdOutBuf.String())
	}

	// Test 3: Run script with shebang (#!/bin/bash) logs preview of first code line, not /bin/bash
	outBuf.Reset()
	cmdOutBuf.Reset()
	scriptBash := `#!/bin/bash
        python3 -c "print('hello_from_bash_script')"
        sleep 0.1
`
	_, _, _, err = lb.Run([]string{"/bin/bash"}, scriptBash)
	if err != nil {
		t.Fatalf("LocalBuilder.Run with bash shebang failed: %v", err)
	}
	if strings.Contains(outBuf.String(), "$ /bin/bash") {
		t.Errorf("expected out NOT to show '$ /bin/bash', got %q", outBuf.String())
	}
	if !strings.Contains(outBuf.String(), "python3 -c") {
		t.Errorf("expected out to show 'python3 -c', got %q", outBuf.String())
	}
}

func TestLocalBuilderSaveGlob(t *testing.T) {
	workDir := t.TempDir()
	outDir := t.TempDir()

	lb := NewLocalBuilder(outDir, true)
	lb.workdir = workDir

	// Setup files in workDir
	if err := lb.WriteFile("foo.txt", "content foo"); err != nil {
		t.Fatal(err)
	}
	if err := lb.WriteFile("bar.txt", "content bar"); err != nil {
		t.Fatal(err)
	}
	if err := lb.WriteFile("ignore.md", "content md"); err != nil {
		t.Fatal(err)
	}

	// Test 1: Save glob with default dst "."
	if err := lb.Save("*.txt", "."); err != nil {
		t.Fatalf("Save('*.txt', '.') failed: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(outDir, "foo.txt")); err != nil || string(b) != "content foo" {
		t.Errorf("expected outDir/foo.txt with 'content foo', got err=%v, content=%q", err, string(b))
	}
	if b, err := os.ReadFile(filepath.Join(outDir, "bar.txt")); err != nil || string(b) != "content bar" {
		t.Errorf("expected outDir/bar.txt with 'content bar', got err=%v, content=%q", err, string(b))
	}
	if _, err := os.Stat(filepath.Join(outDir, "ignore.md")); err == nil {
		t.Errorf("ignore.md should not have been saved")
	}

	// Test 2: Save glob with directory destination "assets/"
	if err := lb.Save("*.txt", "assets/"); err != nil {
		t.Fatalf("Save('*.txt', 'assets/') failed: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(outDir, "assets", "foo.txt")); err != nil || string(b) != "content foo" {
		t.Errorf("expected outDir/assets/foo.txt with 'content foo', got err=%v, content=%q", err, string(b))
	}

	// Test 4: Save directory preserves folder name inside target directory
	subDir := filepath.Join(workDir, "bin")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "tool"), []byte("tool binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := lb.Save("bin", "release/"); err != nil {
		t.Fatalf("Save('bin', 'release/') failed: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(outDir, "release", "bin", "tool")); err != nil || string(b) != "tool binary" {
		t.Errorf("expected outDir/release/bin/tool with 'tool binary', got err=%v, content=%q", err, string(b))
	}

	// Test 5: Save with container override should error (target container locked)
	if err := lb.Save("bin", "other:rel/"); err == nil {
		t.Errorf("expected error for locked container override, got nil")
	}
}

func TestLocalBuilderCopyMatrix(t *testing.T) {
	workDir := t.TempDir()
	srcDir := filepath.Join(workDir, "src")
	if err := os.MkdirAll(filepath.Join(srcDir, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "pkg", "a.txt"), []byte("file a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	lb := NewLocalBuilder(t.TempDir(), true)
	lb.workdir = workDir

	// Copy directory pkg -> ./pkg
	if err := lb.Copy("src/pkg", "."); err != nil {
		t.Fatalf("Copy('src/pkg', '.') failed: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(workDir, "pkg", "a.txt")); err != nil || string(b) != "file a" {
		t.Errorf("expected ./pkg/a.txt, got err=%v, content=%q", err, string(b))
	}

	// Copy single file with rename main.go -> app.go
	if err := lb.Copy("src/main.go", "app.go"); err != nil {
		t.Fatalf("Copy('src/main.go', 'app.go') failed: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(workDir, "app.go")); err != nil || string(b) != "package main" {
		t.Errorf("expected ./app.go, got err=%v, content=%q", err, string(b))
	}
}


