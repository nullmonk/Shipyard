package builder

import (
	"bytes"
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
}

