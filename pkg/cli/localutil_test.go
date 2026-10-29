package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchVersionTags(t *testing.T) {
	cases := []struct {
		pattern, tag string
		want         bool
	}{
		{"V_*", "V_9_9_P2", true},
		{"V_*", "release-1.2", false},
		{"release-*", "release-1.25.0", true},
		{"v*", "v1.3.8", true},
		{"V_*_P[0123456789]", "V_9_9_P2", true},
		{"V_*_P[0123456789]", "V_9_9_PX", false},
		{"*", "anything", true},
		{"", "anything", true},
	}
	for _, c := range cases {
		if got := matchVersionTags(c.pattern, c.tag); got != c.want {
			t.Errorf("matchVersionTags(%q, %q) = %v, want %v", c.pattern, c.tag, got, c.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	ordered := []string{"7.4", "7.8", "8", "9.6p1", "9.9p1", "10.0"}
	for i := 0; i+1 < len(ordered); i++ {
		if compareVersions(ordered[i], ordered[i+1]) >= 0 {
			t.Errorf("expected %s < %s", ordered[i], ordered[i+1])
		}
		if compareVersions(ordered[i+1], ordered[i]) <= 0 {
			t.Errorf("expected %s > %s", ordered[i+1], ordered[i])
		}
	}
	if compareVersions("9.6p1", "9.6p1") != 0 {
		t.Errorf("expected 9.6p1 == 9.6p1")
	}
}

func TestParseLsRemoteTags(t *testing.T) {
	out := "abc123\trefs/tags/V_9_8_P1\n" +
		"def456\trefs/tags/V_9_9_P2\n" +
		"def457\trefs/tags/V_9_9_P2^{}\n" +
		"123abc\trefs/heads/main\n"
	got := parseLsRemoteTags(out)
	if len(got) != 2 || got[0] != "V_9_8_P1" || got[1] != "V_9_9_P2" {
		t.Errorf("parseLsRemoteTags = %v", got)
	}
}

func TestVersionFromArchiveName(t *testing.T) {
	cases := map[string]string{
		"vsftpd-3.0.5.tar.gz":  "3.0.5",
		"vsftpd-2.3.4.tgz":     "2.3.4",
		"openssh-9.6p1.tar.gz": "9.6p1",
		"package-1.2.zip":      "1.2",
		"noversion.tar.gz":     "noversion",
	}
	for in, want := range cases {
		if got := versionFromArchiveName(in); got != want {
			t.Errorf("versionFromArchiveName(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyAndCompareTrees(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, filepath.Join(src, "a.c"), "int main() { return 0; }\n")
	writeTestFile(t, filepath.Join(src, "sub", "b.h"), "#pragma once\n")
	writeTestFile(t, filepath.Join(src, ".git", "config"), "gitdir\n")

	dst := t.TempDir()
	if err := copyTree(src, filepath.Join(dst, "copy"), map[string]bool{".git": true}); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	cpy := filepath.Join(dst, "copy")
	if _, err := os.Stat(filepath.Join(cpy, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should have been excluded from copy")
	}

	diffs, err := compareTrees(src, cpy, map[string]bool{".git": true})
	if err != nil {
		t.Fatalf("compareTrees identical: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("identical trees differ: %v", diffs)
	}

	writeTestFile(t, filepath.Join(cpy, "a.c"), "int main() { return 1; }\n")
	writeTestFile(t, filepath.Join(cpy, "new.c"), "x\n")
	if err := os.Remove(filepath.Join(cpy, "sub", "b.h")); err != nil {
		t.Fatal(err)
	}
	diffs, err = compareTrees(src, cpy, map[string]bool{".git": true})
	if err != nil {
		t.Fatalf("compareTrees changed: %v", err)
	}
	want := map[string]string{"a.c": "modified", "new.c": "added", "sub/b.h": "removed"}
	if len(diffs) != len(want) {
		t.Fatalf("diffs = %v, want %v", diffs, want)
	}
	for _, d := range diffs {
		if want[d.Path] != d.Kind {
			t.Errorf("diff %s = %s, want %s", d.Path, d.Kind, want[d.Path])
		}
	}
}

func TestCheckBalance(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok.c")
	writeTestFile(t, ok, "int f() {\n\tif (1) { return (2); }\n}\n")
	if issues, err := checkBalance(ok); err != nil || len(issues) != 0 {
		t.Errorf("balanced file reported %v, %v", issues, err)
	}

	// Braces inside strings/comments must be ignored.
	tricky := filepath.Join(dir, "tricky.c")
	writeTestFile(t, tricky, "char *s = \"}{\"; // }\n/* { */\nint f() { return 0; }\n")
	if issues, err := checkBalance(tricky); err != nil || len(issues) != 0 {
		t.Errorf("tricky balanced file reported %v, %v", issues, err)
	}

	bad := filepath.Join(dir, "bad.c")
	writeTestFile(t, bad, "int f() {\n\tif (1) {\n\t\treturn 0;\n}\n")
	issues, err := checkBalance(bad)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) == 0 {
		t.Errorf("expected imbalance to be reported")
	} else if !strings.Contains(issues[0].Msg, "unclosed") {
		t.Errorf("expected unclosed report, got %v", issues)
	}

	mismatch := filepath.Join(dir, "mismatch.c")
	writeTestFile(t, mismatch, "int f() {\n\tint x[4);\n}\n")
	issues, err = checkBalance(mismatch)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) == 0 {
		t.Errorf("expected mismatch to be reported")
	}
}
