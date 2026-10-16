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

func TestOpenSSHCodePatchApplication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := builder.Connect(ctx, "")
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

	// Pull source into Dagger
	srcDir, err := builder.PullSource(ctx, client, builder.SourceConfig{
		LocalDir: tmpDir,
	})
	if err != nil {
		t.Fatalf("PullSource failed: %v", err)
	}

	// 2. Snapshot pristine source in container
	ctr := client.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "--no-cache", "git"}).
		WithDirectory("/work/openssh-9.9p1", srcDir).
		WithWorkdir("/work/openssh-9.9p1")

	ctr = builder.SnapshotSource(ctr, "/work/openssh-9.9p1")

	// 3. Run the OpenSSH shipfile against the source in a Python container layer
	shipyardRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	shipfileDir := filepath.Join(shipyardRoot, "examples/openssh")

	patchedDir, err := builder.RunShipfile(ctx, client, ctr.Directory("/work/openssh-9.9p1"), builder.RunShipfileOpts{
		ShipfileDir:    shipfileDir,
		ShipyardPkgDir: shipyardRoot,
		SourceName:     "openssh-9.9p1",
		Version:        "9.9p1",
	})
	if err != nil {
		t.Fatalf("RunShipfile failed: %v", err)
	}

	// 4. Put patched source back into container and generate patch diff
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
