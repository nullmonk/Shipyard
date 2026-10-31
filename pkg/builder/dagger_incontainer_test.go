package builder_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"shipyard/pkg/builder"
	"shipyard/pkg/shipfile"
	shipmod "shipyard/pkg/shipfile/modules/shipyard"
)

// Engine-free coverage for the SHIPYARD_IN_CONTAINER sandbox seam: the
// gpython sandbox hides host and container env (only SHIPYARD_DEPTH is
// injected), so container-driven setup()/build() runs must have the marker
// seeded into the sandbox environ while --local runs must never see it.
// The container-level WithEnvVariable marker in build.go is the other half;
// these tests pin the sandbox half without needing a Dagger engine.

// probeShipfile returns a minimal Builder shipfile whose setup()/build() only
// record os.getenv('SHIPYARD_IN_CONTAINER') into out files (no container ops,
// so a nil-client DaggerBuilder suffices and no engine is required).
func probeShipfile(setupOut, buildOut string) string {
	return fmt.Sprintf(`
from shipyard import Builder
import os

class Shipfile(Builder):
    Name = "in-container-probe"
    Url = "https://github.com/example/repo"

    def setup(self):
        f = open(%s, "w")
        f.write(str(os.getenv("SHIPYARD_IN_CONTAINER")))
        f.close()

    def build(self):
        f = open(%s, "w")
        f.write(str(os.getenv("SHIPYARD_IN_CONTAINER")))
        f.close()

    def save(self):
        pass
`, strconv.Quote(setupOut), strconv.Quote(buildOut))
}

func loadProbe(t *testing.T, src string) (*shipfile.Shipfile, *shipfile.Interpreter) {
	t.Helper()
	interp, err := shipfile.New("")
	if err != nil {
		t.Fatalf("shipfile.New: %v", err)
	}
	t.Cleanup(func() { interp.Close() })
	sf, err := shipfile.ValidateString(src, interp)
	if err != nil {
		t.Fatalf("ValidateString: %v", err)
	}
	if !sf.HasSetup || !sf.HasBuild {
		t.Fatalf("probe shipfile must define setup() and build(), got HasSetup=%v HasBuild=%v", sf.HasSetup, sf.HasBuild)
	}
	return sf, interp
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestIsContainerDrivenWiring(t *testing.T) {
	db := &builder.DaggerBuilder{}
	if !shipmod.IsContainerDriven(db) {
		t.Fatal("expected *DaggerBuilder to report container-driven")
	}
	lb := builder.NewLocalBuilder(t.TempDir(), false)
	if shipmod.IsContainerDriven(lb) {
		t.Fatal("expected *LocalBuilder (--local) to never report container-driven")
	}
	if shipmod.IsContainerDriven(nil) {
		t.Fatal("expected nil builder to report not-container-driven")
	}
}

func TestContainerDrivenSandboxSeedsMarker(t *testing.T) {
	tmpDir := t.TempDir()
	setupOut := filepath.Join(tmpDir, "setup.env")
	buildOut := filepath.Join(tmpDir, "build.env")
	sf, interp := loadProbe(t, probeShipfile(setupOut, buildOut))

	db := &builder.DaggerBuilder{}
	if err := sf.RunSetupWithBuilder(db, shipfile.RunContext{}); err != nil {
		t.Fatalf("RunSetupWithBuilder: %v", err)
	}
	if got := readFile(t, setupOut); got != "1" {
		t.Fatalf("container-driven setup() saw SHIPYARD_IN_CONTAINER=%q, want %q", got, "1")
	}
	if err := sf.RunBuildWithBuilder(db, shipfile.RunContext{}); err != nil {
		t.Fatalf("RunBuildWithBuilder: %v", err)
	}
	if got := readFile(t, buildOut); got != "1" {
		t.Fatalf("container-driven build() saw SHIPYARD_IN_CONTAINER=%q, want %q", got, "1")
	}

	// Duration-scoped: the marker must not leak into the sandbox environ
	// after the phased runs return.
	if _, ok := interp.Sandbox().GetEnv()[shipmod.InContainerEnv]; ok {
		t.Fatalf("sandbox environ leaked %s after container-driven runs", shipmod.InContainerEnv)
	}
}

func TestLocalSandboxOmitsMarker(t *testing.T) {
	tmpDir := t.TempDir()
	setupOut := filepath.Join(tmpDir, "setup.env")
	buildOut := filepath.Join(tmpDir, "build.env")
	sf, interp := loadProbe(t, probeShipfile(setupOut, buildOut))

	// The exact --local path (cli runLocalBuild): RunWithBuilder + LocalBuilder.
	lb := builder.NewLocalBuilder(t.TempDir(), false)
	if err := sf.RunWithBuilder(lb, shipfile.RunContext{}); err != nil {
		t.Fatalf("RunWithBuilder (local): %v", err)
	}
	if got := readFile(t, setupOut); got != "None" {
		t.Fatalf("local setup() saw SHIPYARD_IN_CONTAINER=%q, want %q (absent)", got, "None")
	}
	if got := readFile(t, buildOut); got != "None" {
		t.Fatalf("local build() saw SHIPYARD_IN_CONTAINER=%q, want %q (absent)", got, "None")
	}
	if _, ok := interp.Sandbox().GetEnv()[shipmod.InContainerEnv]; ok {
		t.Fatalf("sandbox environ leaked %s after local runs", shipmod.InContainerEnv)
	}
}
