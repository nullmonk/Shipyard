package incontainer

import (
	"os"
	"strings"
	"testing"
)

func TestResetGenericSourceNoGitInPath(t *testing.T) {
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)

	os.Setenv("PATH", "")
	err := resetGenericSource(t.TempDir())
	if err == nil {
		t.Fatal("expected error when git is not in PATH, got nil")
	}
	if !strings.Contains(err.Error(), "git is not installed") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestResetGenericSourceNoGitSnapshot(t *testing.T) {
	dir := t.TempDir()
	err := resetGenericSource(dir)
	// When git is installed on host, it should fail with missing .git snapshot
	if err == nil {
		t.Fatal("expected error when no .git snapshot, got nil")
	}
	if !strings.Contains(err.Error(), "no git snapshot") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
