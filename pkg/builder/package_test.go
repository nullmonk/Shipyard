package builder_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"shipyard/pkg/builder"
)

func TestSnapshotAndGeneratePatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := builder.Connect(ctx, "")
	if err != nil {
		t.Skipf("skipping test: Dagger connection failed: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	// 1. Setup a container with alpine and git
	sourceDir := "/tmp/build/mypkg-1.0"
	ctr := client.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "--no-cache", "git"}).
		WithExec([]string{"mkdir", "-p", sourceDir}).
		WithNewFile(sourceDir+"/config.c", "char *version = \"1.0\";\n")

	// 2. Snapshot pristine source (git init & commit)
	ctr = builder.SnapshotSource(ctr, sourceDir)

	// 3. Simulate shipfile patcher modifying the source tree
	ctr = ctr.WithNewFile(sourceDir+"/config.c", "char *version = \"1.0-shipyard\";\n")

	// 4. Generate patch diff
	patchContent, err := builder.GeneratePatch(ctx, ctr, sourceDir)
	if err != nil {
		t.Fatalf("GeneratePatch failed: %v", err)
	}

	if !strings.Contains(patchContent, "-char *version = \"1.0\";") ||
		!strings.Contains(patchContent, "+char *version = \"1.0-shipyard\";") {
		t.Fatalf("unexpected patch content: %s", patchContent)
	}

	t.Logf("Generated patch successfully:\n%s", patchContent)
}

