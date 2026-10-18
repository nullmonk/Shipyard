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

