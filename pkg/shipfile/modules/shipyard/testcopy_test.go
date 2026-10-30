package shipmod

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTestCopySrc(t *testing.T) {
	shadow := string(filepath.Separator) + "shadow"
	tests := []struct {
		src      string
		want     string
		wantGlob bool
		wantErr  bool
	}{
		{"out/hello", filepath.Join(shadow, "out", "hello"), false, false},
		{"/abs/path", filepath.Join(shadow, "abs", "path"), false, false},
		{"build:out/hello", filepath.Join(shadow, "out", "hello"), false, false},
		{"build:/abs/path", filepath.Join(shadow, "abs", "path"), false, false},
		{".", shadow, false, false},
		{"", shadow, false, false},
		{"*.deb", filepath.Join(shadow, "*.deb"), true, false},
		{"out/*.rpm", filepath.Join(shadow, "out", "*.rpm"), true, false},
		{"build:out/*.apk", filepath.Join(shadow, "out", "*.apk"), true, false},
		{"other:path", "", false, true},
		{"stage:/x", "", false, true},
	}
	for _, tt := range tests {
		got, isGlob, err := ResolveTestCopySrc(tt.src, shadow)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ResolveTestCopySrc(%q): expected error, got %q", tt.src, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ResolveTestCopySrc(%q): unexpected error: %v", tt.src, err)
			continue
		}
		if got != tt.want || isGlob != tt.wantGlob {
			t.Errorf("ResolveTestCopySrc(%q) = (%q, glob=%v), want (%q, glob=%v)",
				tt.src, got, isGlob, tt.want, tt.wantGlob)
		}
	}
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestCopyFromBuildFile(t *testing.T) {
	shadow := t.TempDir()
	writeTestFile(t, filepath.Join(shadow, "out", "hello"), "binary-bytes", 0o755)

	dstDir := t.TempDir()
	// Plain src resolves against the build shadow.
	if err := copyFromBuild("out/hello", filepath.Join(dstDir, "hello"), shadow); err != nil {
		t.Fatalf("copyFromBuild plain: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dstDir, "hello"))
	if err != nil || string(data) != "binary-bytes" {
		t.Fatalf("copied content = %q, err = %v", data, err)
	}
	if fi, err := os.Stat(filepath.Join(dstDir, "hello")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode not preserved: %v %v", fi, err)
	}

	// Explicit build: prefix resolves identically.
	if err := copyFromBuild("build:out/hello", filepath.Join(dstDir, "hello2"), shadow); err != nil {
		t.Fatalf("copyFromBuild build: prefix: %v", err)
	}

	// dst naming an existing directory receives the basename.
	sub := filepath.Join(dstDir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFromBuild("out/hello", sub+"/", shadow); err != nil {
		t.Fatalf("copyFromBuild to dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sub, "hello")); err != nil {
		t.Fatalf("expected file in dst dir: %v", err)
	}

	// Unknown stage is rejected.
	if err := copyFromBuild("other:out/hello", filepath.Join(dstDir, "x"), shadow); err == nil {
		t.Fatal("expected error for unknown stage, got nil")
	}

	// Missing source is an error.
	if err := copyFromBuild("out/missing", filepath.Join(dstDir, "x"), shadow); err == nil {
		t.Fatal("expected error for missing source, got nil")
	}
}

func TestCopyFromBuildDirAndGlob(t *testing.T) {
	shadow := t.TempDir()
	writeTestFile(t, filepath.Join(shadow, "rpms", "a", "f1.txt"), "1", 0o644)
	writeTestFile(t, filepath.Join(shadow, "rpms", "a", "f2.txt"), "2", 0o644)

	dstDir := t.TempDir()
	if err := copyFromBuild("rpms", filepath.Join(dstDir, "rpms"), shadow); err != nil {
		t.Fatalf("copyFromBuild dir: %v", err)
	}
	for _, f := range []string{"f1.txt", "f2.txt"} {
		if _, err := os.Stat(filepath.Join(dstDir, "rpms", "a", f)); err != nil {
			t.Fatalf("expected %s after dir copy: %v", f, err)
		}
	}

	globDst := filepath.Join(dstDir, "glob")
	if err := copyFromBuild("rpms/a/*.txt", globDst+"/", shadow); err != nil {
		t.Fatalf("copyFromBuild glob: %v", err)
	}
	for _, f := range []string{"f1.txt", "f2.txt"} {
		if _, err := os.Stat(filepath.Join(globDst, f)); err != nil {
			t.Fatalf("expected %s after glob copy: %v", f, err)
		}
	}

	// Zero glob matches is an error (mirrors Save's no-match error so
	// SaveIfExists-style composition keeps working).
	if err := copyFromBuild("rpms/a/*.nonexistent", globDst+"/", shadow); err == nil {
		t.Fatal("expected error for zero-match glob, got nil")
	}
}

func TestTestCopyAvailableAt(t *testing.T) {
	if testCopyAvailableAt(filepath.Join(t.TempDir(), "does-not-exist")) {
		t.Error("expected false for missing dir")
	}
	if !testCopyAvailableAt(t.TempDir()) {
		t.Error("expected true for existing dir")
	}
}
