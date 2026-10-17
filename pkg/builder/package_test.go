package builder_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"shipyard/pkg/builder"
)

func TestSnapshotAndGeneratePatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := builder.Connect(ctx, "", false)
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

func TestInteractiveShellSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := builder.Connect(ctx, "", false)
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

	tmpDir, err := os.MkdirTemp("", "sy-sh-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "s.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var outBuf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		cmdStr := []byte("echo HELLO_FROM_PTY && exit\n")
		var hdr [5]byte
		hdr[0] = 0 // shellMsgData
		binary.BigEndian.PutUint32(hdr[1:5], uint32(len(cmdStr)))
		_, _ = conn.Write(hdr[:])
		_, _ = conn.Write(cmdStr)

		_, _ = io.Copy(&outBuf, conn)
	}()

	selfFile := client.Host().File(binPath)
	hostSock := client.Host().UnixSocket(sockPath)
	containerSock := "/tmp/.shipyard-shell.sock"

	_, err = client.Container().
		From("alpine:latest").
		WithFile("/usr/local/bin/shipyard", selfFile, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithEnvVariable("SHIPYARD_IN_CONTAINER", "1").
		WithUnixSocket(containerSock, hostSock).
		WithExec([]string{"shipyard", "shell", containerSock}).
		Sync(ctx)
	if err != nil {
		t.Fatalf("shipyard shell failed: %v", err)
	}
	<-done

	if !strings.Contains(outBuf.String(), "HELLO_FROM_PTY") {
		t.Fatalf("expected PTY output to contain HELLO_FROM_PTY, got: %q", outBuf.String())
	}
}

func TestSnapshotSourceWithoutGit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	engine, err := builder.Connect(ctx, "", false)
	if err != nil {
		t.Skipf("skipping test: Dagger connection failed: %v", err)
	}
	defer engine.Close()

	client := engine.Client()

	sourceDir := "/tmp/build"
	ctr := client.Container().
		From("alpine:latest").
		WithExec([]string{"mkdir", "-p", sourceDir}).
		WithNewFile(sourceDir+"/file.txt", "hello")

	// SnapshotSource without git installed should be a no-op and not fail
	ctr = builder.SnapshotSource(ctr, sourceDir)
	if _, err := ctr.Sync(ctx); err != nil {
		t.Fatalf("SnapshotSource failed when git is not installed: %v", err)
	}
}



