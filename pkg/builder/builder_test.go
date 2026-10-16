package builder_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shipyard/pkg/builder"
)

func TestDaggerBuildPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := builder.Connect(ctx, "")
	if err != nil {
		t.Skipf("skipping Dagger test: failed to connect to Dagger engine: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	// 1. Create a temporary source project directory on host
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
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(helloGo), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Step 1: Pull source code into Dagger directory
	srcDir, err := builder.PullSource(ctx, client, builder.SourceConfig{
		LocalDir: tempDir,
	})
	if err != nil {
		t.Fatalf("PullSource failed: %v", err)
	}

	// 3. Step 2: Run Python interpreter in a container layer on the source code
	patchScript := `
with open('main.go', 'r') as f:
    content = f.read()
content = content.replace('"unpatched"', '"patched by python interpreter"')
with open('main.go', 'w') as f:
    f.write(content)
print("successfully patched main.go")
`
	patchedSource, stdout, err := builder.RunPython(ctx, client, srcDir, "/work/hello", patchScript, nil, "python:3-slim")
	if err != nil {
		t.Fatalf("RunPython failed: %v", err)
	}
	t.Logf("Python output: %s", stdout)

	// 4. Step 3 & 4: Setting up build layers and building it
	outputDir, err := os.MkdirTemp("", "shipyard-test-out-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outputDir)

	res, err := builder.BuildGeneric(ctx, client, builder.BuildOpts{
		BaseImage:       "golang:1.22-alpine",
		SourceName:      "hello",
		Source:          patchedSource,
		Setup:           "apk add --no-cache file",
		Build:           "go build -o hello main.go",
		ArtifactPattern: `^hello$`,
		OutputDir:       outputDir,
	})
	if err != nil {
		t.Fatalf("BuildGeneric failed: %v", err)
	}

	if len(res.Artifacts) == 0 {
		t.Fatalf("expected artifacts, got none")
	}

	// Verify that the built binary exists on the host in outputDir
	binPath := filepath.Join(outputDir, "hello")
	fi, err := os.Stat(binPath)
	if err != nil {
		t.Fatalf("expected built binary at %s, error: %v", binPath, err)
	}
	if fi.Size() == 0 {
		t.Fatalf("binary is 0 bytes")
	}

	t.Logf("Successfully built binary %s (size %d bytes)", binPath, fi.Size())
}

