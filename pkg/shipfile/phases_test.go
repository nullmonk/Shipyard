package shipfile_test

import (
	"testing"

	"shipyard/pkg/shipfile"
)

// TestRunSetupBuildSplitPhases verifies the phased-build split: setup-only,
// build-only, and the combined RunWithBuilder drive the same methods in the
// same order with identical error wrapping.
func TestRunSetupBuildSplitPhases(t *testing.T) {
	interp := newTestInterpreter(t)

	src := `from shipyard import Builder, From
class Shipfile(Builder):
    Name = "phases"
    def setup(self):
        From("alpine:3.19", "s")
    def build(self):
        From("alpine:3.20", "b")
    def save(self):
        pass
`
	sf, err := shipfile.LoadString(src, interp)
	if err != nil {
		t.Fatalf("LoadString: %v", err)
	}
	if !sf.HasSetup || !sf.HasBuild || !sf.HasSave {
		t.Fatalf("expected HasSetup/HasBuild/HasSave, got %v/%v/%v", sf.HasSetup, sf.HasBuild, sf.HasSave)
	}

	setupOnly := &mockBuilder{}
	if err := sf.RunSetupWithBuilder(setupOnly, shipfile.RunContext{}); err != nil {
		t.Fatalf("RunSetupWithBuilder: %v", err)
	}
	if len(setupOnly.froms) != 1 || setupOnly.froms[0] != "alpine:3.19" {
		t.Errorf("setup-only froms = %v, want [alpine:3.19]", setupOnly.froms)
	}

	buildOnly := &mockBuilder{}
	if err := sf.RunBuildWithBuilder(buildOnly, shipfile.RunContext{}); err != nil {
		t.Fatalf("RunBuildWithBuilder: %v", err)
	}
	if len(buildOnly.froms) != 1 || buildOnly.froms[0] != "alpine:3.20" {
		t.Errorf("build-only froms = %v, want [alpine:3.20]", buildOnly.froms)
	}

	combined := &mockBuilder{}
	if err := sf.RunWithBuilder(combined, shipfile.RunContext{}); err != nil {
		t.Fatalf("RunWithBuilder: %v", err)
	}
	if len(combined.froms) != 2 || combined.froms[0] != "alpine:3.19" || combined.froms[1] != "alpine:3.20" {
		t.Errorf("combined froms = %v, want [alpine:3.19 alpine:3.20]", combined.froms)
	}
	if len(combined.saves) != 0 {
		// save() is `pass`: no Save calls recorded, but it must not error.
		t.Errorf("combined saves = %v, want none", combined.saves)
	}
}

// TestRunSetupBuildSplitErrors verifies the split preserves RunWithBuilder's
// error wrapping ("setup: ...", "build: ...").
func TestRunSetupBuildSplitErrors(t *testing.T) {
	interp := newTestInterpreter(t)

	src := `from shipyard import Builder
class Shipfile(Builder):
    Name = "phases-err"
    def setup(self):
        raise ValueError("boom-setup")
    def build(self):
        raise ValueError("boom-build")
`
	sf, err := shipfile.LoadString(src, interp)
	if err != nil {
		t.Fatalf("LoadString: %v", err)
	}

	if err := sf.RunSetupWithBuilder(&mockBuilder{}, shipfile.RunContext{}); err == nil {
		t.Fatal("expected setup error, got nil")
	} else if got := err.Error(); len(got) < 6 || got[:6] != "setup:" {
		t.Errorf("setup error = %q, want prefix %q", got, "setup:")
	}

	// Build-only on a shipfile whose setup fails must still run build (phases
	// are independent calls) and report the build failure.
	if err := sf.RunBuildWithBuilder(&mockBuilder{}, shipfile.RunContext{}); err == nil {
		t.Fatal("expected build error, got nil")
	} else if got := err.Error(); len(got) < 6 || got[:6] != "build:" {
		t.Errorf("build error = %q, want prefix %q", got, "build:")
	}

	if err := sf.RunWithBuilder(&mockBuilder{}, shipfile.RunContext{}); err == nil {
		t.Fatal("expected combined error, got nil")
	} else if got := err.Error(); len(got) < 6 || got[:6] != "setup:" {
		t.Errorf("combined error = %q, want prefix %q (setup runs first)", got, "setup:")
	}
}
