package builder

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDropIntoShellBareContainer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping test: Dagger connection failed: %v", err)
	}
	defer engine.Close()

	client := engine.Client()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(root, "bin/shipyard")
	if _, err := os.Stat(binPath); err != nil {
		t.Skipf("bin/shipyard not found: %v", err)
	}

	// Bare container with no shipyard binary injected initially
	ctr := client.Container().From("alpine:latest")

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outR.Close()

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()
	os.Stdin = inR
	os.Stdout = outW

	go func() {
		defer inW.Close()
		// Give the shell session a moment to connect and become ready
		time.Sleep(500 * time.Millisecond)
		_, _ = inW.Write([]byte("echo DROP_INTO_SHELL_OK && exit\n"))
	}()

	var logBuf bytes.Buffer
	dropIntoShell(ctx, client, &logBuf, ctr, root)
	_ = outW.Close()

	var outBuf bytes.Buffer
	_, _ = io.Copy(&outBuf, outR)

	combined := outBuf.String() + "\n" + logBuf.String()
	if !strings.Contains(combined, "DROP_INTO_SHELL_OK") {
		t.Fatalf("expected output to contain DROP_INTO_SHELL_OK, got output:\n%s", combined)
	}
}

