package builder_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shipyard/pkg/builder"
	"shipyard/pkg/shipfile"
)

func TestOpenSSHCodePatchApplication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := builder.Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping test: Dagger connection failed: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	// 1. Create a mock source directory with servconf.c
	tmpDir, err := os.MkdirTemp("", "shipyard-openssh-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	unpatchedServconf := `/* OpenSSH mock servconf.c */
#include <stdio.h>

void fill_default_options(ServerOptions *options) {
	if (options->version_addendum == NULL)
		options->version_addendum = xstrdup("");
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "servconf.c"), []byte(unpatchedServconf), 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.MkdirTemp("", "shipyard-openssh-orig-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(origDir)

	if err := os.WriteFile(filepath.Join(origDir, "servconf.c"), []byte(unpatchedServconf), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pull source into Dagger
	srcDir, err := builder.PullSource(ctx, client, builder.SourceConfig{
		LocalDir: origDir,
	})
	if err != nil {
		t.Fatalf("PullSource failed: %v", err)
	}

	// 2. Apply code patches locally via the Go shipfile interpreter
	shipyardRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	shipfileDir := filepath.Join(shipyardRoot, "examples/openssh")

	interp, err := shipfile.New(shipyardRoot)
	if err != nil {
		t.Fatalf("shipyard.New failed: %v", err)
	}
	defer interp.Close()

	sf, err := shipfile.Load(filepath.Join(shipfileDir, "shipfile.py"), interp)
	if err != nil {
		t.Fatalf("shipfile.Load failed: %v", err)
	}
	if _, err := sf.RunCodePatches(tmpDir, "9.9p1", io.Discard); err != nil {
		t.Fatalf("RunCodePatches failed: %v", err)
	}

	// 3. Load patched source into Dagger and generate patch diff
	patchedDir, err := builder.PullSource(ctx, client, builder.SourceConfig{LocalDir: tmpDir})
	if err != nil {
		t.Fatalf("PullSource (patched) failed: %v", err)
	}

	ctr := client.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "--no-cache", "git"}).
		WithDirectory("/work/openssh-9.9p1", srcDir).
		WithWorkdir("/work/openssh-9.9p1")

	ctr = builder.SnapshotSource(ctr, "/work/openssh-9.9p1")
	ctr = ctr.WithDirectory("/work/openssh-9.9p1", patchedDir)
	patchContent, err := builder.GeneratePatch(ctx, ctr, "/work/openssh-9.9p1")
	if err != nil {
		t.Fatalf("GeneratePatch failed: %v", err)
	}

	t.Logf("Generated Patch Diff:\n%s", patchContent)

	// 5. Verify the code patch applied properly
	expectedOld := `options->version_addendum = xstrdup("");`
	expectedNew := `options->version_addendum = xstrdup("opensource is good");`

	if !strings.Contains(patchContent, "-") || !strings.Contains(patchContent, expectedOld) {
		t.Errorf("patch diff missing expected removal: %s", expectedOld)
	}
	if !strings.Contains(patchContent, "+") || !strings.Contains(patchContent, expectedNew) {
		t.Errorf("patch diff missing expected addition: %s", expectedNew)
	}

	// Also verify file content directly inside patched directory
	patchedContent, err := ctr.File("/work/openssh-9.9p1/servconf.c").Contents(ctx)
	if err != nil {
		t.Fatalf("failed to read patched servconf.c: %v", err)
	}

	if !strings.Contains(patchedContent, `options->version_addendum = xstrdup("opensource is good");`) {
		t.Errorf("servconf.c does not contain substituted banner: %s", patchedContent)
	}
}
