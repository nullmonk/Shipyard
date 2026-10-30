package builder

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestPhaseMarkersExact(t *testing.T) {
	if PhaseSetup != "[drydock:phase:setup]" {
		t.Errorf("PhaseSetup = %q", PhaseSetup)
	}
	if PhaseBuild != "[drydock:phase:build]" {
		t.Errorf("PhaseBuild = %q", PhaseBuild)
	}
	if PhasePublish != "[drydock:phase:publish]" {
		t.Errorf("PhasePublish = %q", PhasePublish)
	}
	if PhaseDone != "[drydock:done]" {
		t.Errorf("PhaseDone = %q", PhaseDone)
	}
	if got := TestPhaseMarker("build"); got != "[drydock:phase:test:build]" {
		t.Errorf("TestPhaseMarker(build) = %q", got)
	}
	// The <label> is the raw --test-system value, verbatim.
	if got := TestPhaseMarker("ubuntu:22.04"); got != "[drydock:phase:test:ubuntu:22.04]" {
		t.Errorf("TestPhaseMarker(ubuntu:22.04) = %q", got)
	}
}

func TestTestLabels(t *testing.T) {
	if got := TestLabels(nil); len(got) != 1 || got[0] != "build" {
		t.Errorf("TestLabels(nil) = %v, want [build]", got)
	}
	if got := TestLabels([]string{}); len(got) != 1 || got[0] != "build" {
		t.Errorf("TestLabels([]) = %v, want [build]", got)
	}
	systems := []string{"ubuntu:22.04", "alpine:3.20"}
	got := TestLabels(systems)
	if len(got) != 2 || got[0] != "ubuntu:22.04" || got[1] != "alpine:3.20" {
		t.Errorf("TestLabels(%v) = %v", systems, got)
	}
	// Must be a copy: mutating the result must not affect the input.
	got[0] = "mutated"
	if systems[0] != "ubuntu:22.04" {
		t.Errorf("TestLabels aliases input: %v", systems)
	}
}

func TestMarkerEmissionOrderLegacy(t *testing.T) {
	var buf bytes.Buffer
	EmitMarker(&buf, PhaseSetup)
	EmitMarker(&buf, PhaseBuild)
	for _, l := range TestLabels(nil) {
		EmitMarker(&buf, TestPhaseMarker(l))
	}
	EmitMarker(&buf, PhasePublish)
	EmitMarker(&buf, PhaseDone)

	want := "[drydock:phase:setup]\n" +
		"[drydock:phase:build]\n" +
		"[drydock:phase:test:build]\n" +
		"[drydock:phase:publish]\n" +
		"[drydock:done]\n"
	if buf.String() != want {
		t.Errorf("legacy marker order mismatch:\n got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestMarkerEmissionOrderFanOut(t *testing.T) {
	var buf bytes.Buffer
	systems := []string{"ubuntu:22.04", "alpine:3.20"}
	EmitMarker(&buf, PhaseSetup)
	EmitMarker(&buf, PhaseBuild)
	for _, l := range TestLabels(systems) {
		EmitMarker(&buf, TestPhaseMarker(l))
	}
	EmitMarker(&buf, PhasePublish)
	EmitMarker(&buf, PhaseDone)

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	want := []string{
		"[drydock:phase:setup]",
		"[drydock:phase:build]",
		"[drydock:phase:test:ubuntu:22.04]",
		"[drydock:phase:test:alpine:3.20]",
		"[drydock:phase:publish]",
		"[drydock:done]",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d marker lines %v, want %d %v", len(lines), lines, len(want), want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestPhaseOutNilFallsBackToStdout(t *testing.T) {
	if PhaseOut(nil) == nil {
		t.Error("PhaseOut(nil) must not be nil")
	}
	var buf bytes.Buffer
	var w io.Writer = &buf
	if PhaseOut(w) != w {
		t.Error("PhaseOut(buf) must return buf")
	}
}
