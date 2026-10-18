package builder

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDaggerBuilderCopyGlobsAndDirs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping Dagger test: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "go.sum"), []byte("// sum\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, "cmd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "cmd", "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	src, err := PullSource(ctx, client, SourceConfig{LocalDir: tempDir})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	db := &DaggerBuilder{
		ctx:     ctx,
		client:  client,
		source:  src,
		out:     &out,
		cmdOut:  &out,
		cmdErr:  &out,
		verbose: true,
	}

	if err := db.From("alpine:3.19", ""); err != nil {
		t.Fatalf("From failed: %v", err)
	}
	if err := db.Workdir("/app"); err != nil {
		t.Fatalf("Workdir failed: %v", err)
	}

	// Copy directory cmd -> ./cmd
	if err := db.Copy("cmd", ".", ""); err != nil {
		t.Fatalf("Copy('cmd', '.') failed: %v", err)
	}

	// Copy glob pattern go.* -> ./go.mod, ./go.sum
	if err := db.Copy("go.*", ".", ""); err != nil {
		t.Fatalf("Copy('go.*', '.') failed: %v", err)
	}

	stdout, stderr, rc, err := db.Run([]string{"ls", "-la", "/app"}, "")
	if err != nil || rc != 0 {
		t.Fatalf("Run ls failed (rc=%d, err=%v): %s\n%s", rc, err, stdout, stderr)
	}

	if !strings.Contains(stdout, "go.mod") {
		t.Errorf("expected go.mod in /app, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "go.sum") {
		t.Errorf("expected go.sum in /app, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "cmd") {
		t.Errorf("expected cmd in /app, got:\n%s", stdout)
	}

	// Verify cmd/main.go exists inside container
	stdout, _, rc, err = db.Run([]string{"cat", "/app/cmd/main.go"}, "")
	if err != nil || rc != 0 || !strings.Contains(stdout, "package main") {
		t.Errorf("expected cmd/main.go with 'package main', got rc=%d: %s", rc, stdout)
	}
}

func TestDaggerBuilderStageCopyFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping Dagger test: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	var out bytes.Buffer
	db := &DaggerBuilder{
		ctx:     ctx,
		client:  client,
		out:     &out,
		cmdOut:  &out,
		cmdErr:  &out,
		verbose: true,
	}

	if err := db.From("alpine:3.19", "builder"); err != nil {
		t.Fatalf("From('builder') failed: %v", err)
	}
	if err := db.WriteFile("/out/hello.txt", "hello from builder stage"); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := db.From("alpine:3.19", "final"); err != nil {
		t.Fatalf("From('final') failed: %v", err)
	}
	if err := db.Copy("/out/hello.txt", "/usr/local/bin/hello.txt", "builder"); err != nil {
		t.Fatalf("Copy across stages failed: %v", err)
	}

	stdout, stderr, rc, err := db.Run([]string{"cat", "/usr/local/bin/hello.txt"}, "")
	if err != nil || rc != 0 {
		t.Fatalf("Run cat failed (rc=%d, err=%v): %s\n%s", rc, err, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "hello from builder stage" {
		t.Errorf("expected 'hello from builder stage', got %q", stdout)
	}
}

func TestDaggerBuilderStagePrefixCopy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping Dagger test: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	var out bytes.Buffer
	db := &DaggerBuilder{
		ctx:     ctx,
		client:  client,
		out:     &out,
		cmdOut:  &out,
		cmdErr:  &out,
		verbose: true,
	}

	if err := db.From("alpine:3.19", "builder"); err != nil {
		t.Fatalf("From('builder') failed: %v", err)
	}
	if err := db.WriteFile("/out/hello.txt", "hello prefix syntax"); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := db.From("alpine:3.19", "final"); err != nil {
		t.Fatalf("From('final') failed: %v", err)
	}
	// Test Copy with "builder:/out/hello.txt" stage prefix
	if err := db.Copy("builder:/out/hello.txt", "/usr/local/bin/hello.txt"); err != nil {
		t.Fatalf("Copy with stage prefix failed: %v", err)
	}

	stdout, stderr, rc, err := db.Run([]string{"cat", "/usr/local/bin/hello.txt"}, "")
	if err != nil || rc != 0 {
		t.Fatalf("Run cat failed (rc=%d, err=%v): %s\n%s", rc, err, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "hello prefix syntax" {
		t.Errorf("expected 'hello prefix syntax', got %q", stdout)
	}

	// Test Save with container override should fail (target container locked)
	if err := db.Save("/out/hello.txt", "other:rel/"); err == nil {
		t.Errorf("expected error when overriding locked target container in Save, got nil")
	}
}

func TestPullSourceDockerignoreWhitelist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping Dagger test: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	tempDir := t.TempDir()
	os.WriteFile(filepath.Join(tempDir, "justfile"), []byte("all:\n"), 0644)
	os.WriteFile(filepath.Join(tempDir, "userdb.c"), []byte("int main(){}\n"), 0644)
	os.WriteFile(filepath.Join(tempDir, "ignored.txt"), []byte("ignored\n"), 0644)
	os.Mkdir(filepath.Join(tempDir, "test"), 0755)
	os.WriteFile(filepath.Join(tempDir, "test", "smoke.py"), []byte("print(1)\n"), 0644)
	os.WriteFile(filepath.Join(tempDir, ".dockerignore"), []byte("*\n!justfile\n!userdb.c\n!test/*\n"), 0644)

	dir, err := PullSource(ctx, client, SourceConfig{LocalDir: tempDir})
	if err != nil {
		t.Fatalf("PullSource failed: %v", err)
	}

	entries, err := dir.Entries(ctx)
	if err != nil {
		t.Fatalf("Entries failed: %v", err)
	}
	t.Logf("Entries from PullSource: %v", entries)

	foundJustfile := false
	foundUserdb := false
	foundTest := false
	foundIgnored := false

	for _, e := range entries {
		switch e {
		case "justfile":
			foundJustfile = true
		case "userdb.c":
			foundUserdb = true
		case "test", "test/":
			foundTest = true
		case "ignored.txt":
			foundIgnored = true
		}
	}

	if !foundJustfile {
		t.Errorf("expected justfile in entries, got: %v", entries)
	}
	if !foundUserdb {
		t.Errorf("expected userdb.c in entries, got: %v", entries)
	}
	if !foundTest {
		t.Errorf("expected test/ in entries, got: %v", entries)
	}
	if foundIgnored {
		t.Errorf("ignored.txt should NOT be in entries, got: %v", entries)
	}

	if _, err := dir.File("test/smoke.py").Contents(ctx); err != nil {
		t.Fatalf("Contents of test/smoke.py failed: %v", err)
	}
}

func TestDaggerBuilderSaveGlob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping Dagger test: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	var out bytes.Buffer
	db := &DaggerBuilder{
		ctx:     ctx,
		client:  client,
		out:     &out,
		cmdOut:  &out,
		cmdErr:  &out,
		verbose: true,
	}

	if err := db.From("alpine:3.19", ""); err != nil {
		t.Fatalf("From failed: %v", err)
	}
	if err := db.Workdir("/app"); err != nil {
		t.Fatalf("Workdir failed: %v", err)
	}
	if err := db.WriteFile("/app/output/libfoo.so", "binary foo"); err != nil {
		t.Fatalf("WriteFile libfoo.so failed: %v", err)
	}
	if err := db.WriteFile("/app/output/libbar.so", "binary bar"); err != nil {
		t.Fatalf("WriteFile libbar.so failed: %v", err)
	}
	if err := db.WriteFile("/app/output/readme.txt", "readme text"); err != nil {
		t.Fatalf("WriteFile readme.txt failed: %v", err)
	}

	// Test 1: Save glob with default dst "."
	if err := db.Save("output/*.so", "."); err != nil {
		t.Fatalf("Save('output/*.so', '.') failed: %v", err)
	}

	// Test 2: Save single file with dir dst "assets/"
	if err := db.Save("output/readme.txt", "assets/"); err != nil {
		t.Fatalf("Save('output/readme.txt', 'assets/') failed: %v", err)
	}

	tempOutDir := t.TempDir()
	matches, err := exportArtifacts(ctx, db.Saves, tempOutDir, true, &out)
	if err != nil {
		t.Fatalf("exportArtifacts failed: %v", err)
	}

	if len(matches) != 3 {
		t.Fatalf("expected 3 exported artifacts, got %d: %v", len(matches), matches)
	}

	if b, err := os.ReadFile(filepath.Join(tempOutDir, "libfoo.so")); err != nil || string(b) != "binary foo" {
		t.Errorf("expected tempOutDir/libfoo.so with 'binary foo', got err=%v, content=%q", err, string(b))
	}
	if b, err := os.ReadFile(filepath.Join(tempOutDir, "libbar.so")); err != nil || string(b) != "binary bar" {
		t.Errorf("expected tempOutDir/libbar.so with 'binary bar', got err=%v, content=%q", err, string(b))
	}
	if b, err := os.ReadFile(filepath.Join(tempOutDir, "assets", "readme.txt")); err != nil || string(b) != "readme text" {
		t.Errorf("expected tempOutDir/assets/readme.txt with 'readme text', got err=%v, content=%q", err, string(b))
	}
}

func TestDaggerBuilderCopyDefaultDst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping Dagger test: %v", err)
	}
	defer engine.Close()

	client := engine.Client()
	tempSrc := t.TempDir()
	os.WriteFile(filepath.Join(tempSrc, "hello.txt"), []byte("hello from src"), 0644)
	src := client.Host().Directory(tempSrc)

	var out bytes.Buffer
	db := &DaggerBuilder{
		ctx:     ctx,
		client:  client,
		source:  src,
		out:     &out,
		cmdOut:  &out,
		cmdErr:  &out,
		verbose: true,
	}

	if err := db.From("alpine:3.19", ""); err != nil {
		t.Fatalf("From failed: %v", err)
	}
	if err := db.Workdir("/workspace"); err != nil {
		t.Fatalf("Workdir failed: %v", err)
	}

	// Copy "." without specifying a separate dst (defaults to "." which maps to Workdir)
	if err := db.Copy(".", ".", ""); err != nil {
		t.Fatalf("Copy('.', '.') failed: %v", err)
	}

	stdout, stderr, rc, err := db.Run([]string{"cat", "/workspace/hello.txt"}, "")
	if err != nil || rc != 0 {
		t.Fatalf("Run cat failed (rc=%d, err=%v): %s\n%s", rc, err, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "hello from src" {
		t.Errorf("expected 'hello from src', got %q", stdout)
	}
}

