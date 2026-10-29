package shipfile_test

import (
	"path/filepath"
	"testing"

	"shipyard/pkg/shipfile"
)

func loadOpensshExample(t *testing.T) *shipfile.Shipfile {
	t.Helper()
	interp, err := shipfile.New("")
	if err != nil {
		t.Fatalf("shipfile.New: %v", err)
	}
	t.Cleanup(func() { interp.Close() })
	sf, err := shipfile.Load(filepath.Join("..", "..", "examples", "openssh", "shipfile.py"), interp)
	if err != nil {
		t.Fatalf("load examples/openssh/shipfile.py: %v", err)
	}
	return sf
}

func TestTagVersionRoundTrip(t *testing.T) {
	sf := loadOpensshExample(t)
	ver, err := sf.TagToVersion("V_9_9_P2")
	if err != nil {
		t.Fatalf("TagToVersion: %v", err)
	}
	if ver != "9.9p2" {
		t.Errorf("TagToVersion(V_9_9_P2) = %q, want %q", ver, "9.9p2")
	}
	tag, err := sf.VersionToTag("9.9p2")
	if err != nil {
		t.Fatalf("VersionToTag: %v", err)
	}
	if tag != "V_9_9_P2" {
		t.Errorf("VersionToTag(9.9p2) = %q, want %q", tag, "V_9_9_P2")
	}
}

func TestIsVersionIgnored(t *testing.T) {
	sf := loadOpensshExample(t)
	// examples/openssh ignores anything pre-7.0.
	ignored, err := sf.IsVersionIgnored("6.0")
	if err != nil {
		t.Fatalf("IsVersionIgnored: %v", err)
	}
	if !ignored {
		t.Errorf("expected 6.0 to be ignored")
	}
	ignored, err = sf.IsVersionIgnored("9.9p1")
	if err != nil {
		t.Fatalf("IsVersionIgnored: %v", err)
	}
	if ignored {
		t.Errorf("expected 9.9p1 to be kept")
	}
}

func TestVersionFuncErrors(t *testing.T) {
	sf := loadOpensshExample(t)
	// Old/pre-release tag scheme maps to "0.0" rather than erroring.
	if ver, err := sf.TagToVersion("V_1_2_3_PRE5"); err != nil || ver != "0.0" {
		t.Errorf("TagToVersion(V_1_2_3_PRE5) = %q, %v; want 0.0, nil", ver, err)
	}
}
