package builder_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shipyard/pkg/builder"
)

func TestDaggerBuildPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := builder.Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping Dagger test: failed to connect to Dagger engine: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	// Create a temporary Go source project and patch it
	tempDir, err := os.MkdirTemp("", "shipyard-test-src-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	helloGo := `package main

import "fmt"

var Message = "unpatched"

func main() {
	fmt.Println("Message:", Message)
}
`
	patchedGo := strings.ReplaceAll(helloGo, `"unpatched"`, `"patched"`)
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(patchedGo), 0o644); err != nil {
		t.Fatal(err)
	}

	src, err := builder.PullSource(ctx, client, builder.SourceConfig{LocalDir: tempDir})
	if err != nil {
		t.Fatalf("PullSource failed: %v", err)
	}

	outputDir, err := os.MkdirTemp("", "shipyard-test-out-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outputDir)

	const workdir = "/work/hello"
	ctr := client.Container().
		From("golang:1.22-alpine").
		WithDirectory(workdir, src).
		WithWorkdir(workdir).
		WithExec([]string{"go", "build", "-o", "hello", "main.go"})

	files, err := builder.FindFiles(ctx, ctr, workdir)
	if err != nil {
		t.Fatalf("FindFiles failed: %v", err)
	}

	_, matches, err := builder.ExportArtifacts(ctx, ctr, files, workdir, `^hello$`, outputDir, nil)
	if err != nil {
		t.Fatalf("ExportArtifacts failed: %v", err)
	}

	if len(matches) == 0 {
		t.Fatalf("expected artifacts, got none")
	}

	binPath := filepath.Join(outputDir, "hello")
	fi, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("expected built binary at %s: %v", binPath, err)
	}
	if fi.Size() == 0 {
		t.Fatalf("binary is 0 bytes")
	}

	t.Logf("built binary %s (%d bytes)", binPath, fi.Size())
}
