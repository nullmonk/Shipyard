package builder

import (
	"fmt"
	"io"
	"os"
)

// Drydock phased-job log markers.
//
// A phased `shipyard build` run brackets each phase with exactly one marker
// line on STDOUT (the builder's Stdout writer):
//
//	[drydock:phase:setup]    driver/shipfile setup
//	[drydock:phase:build]    image build (patch/apply/compile or build())
//	[drydock:phase:test:<label>]  @test run; <label> is "build" for the
//	                             legacy in-place run, else the --test-system
//	                             image ref for that fresh test container.
//	[drydock:phase:publish]  artifact export to the output directory
//	[drydock:done]           overall success (absent when a phase fails)
//
// Text before the first marker is preamble. Any phase failure (including a
// failure in any single test system) fails the overall run: no [drydock:done]
// line is emitted and the build returns a non-nil error.
const (
	PhaseSetup   = "[drydock:phase:setup]"
	PhaseBuild   = "[drydock:phase:build]"
	PhasePublish = "[drydock:phase:publish]"
	PhaseDone    = "[drydock:done]"

	// LegacyTestLabel is the <label> used for the in-place test run when no
	// --test-system flag is given (legacy single-system behavior).
	LegacyTestLabel = "build"
)

// TestPhaseMarker returns the exact marker line for a test phase labeled
// with label (either LegacyTestLabel or a --test-system image ref).
func TestPhaseMarker(label string) string {
	return "[drydock:phase:test:" + label + "]"
}

// TestLabels returns the ordered test-phase labels for a build: ["build"]
// when no --test-system flags were given (legacy in-place run), otherwise
// one label per --test-system image ref in flag order.
func TestLabels(systems []string) []string {
	if len(systems) == 0 {
		return []string{LegacyTestLabel}
	}
	out := make([]string, len(systems))
	copy(out, systems)
	return out
}

// PhaseOut resolves the writer that receives phase markers. Markers must land
// on stdout, so this is the builder's Stdout writer (never the progress/stderr
// writer). A nil writer falls back to os.Stdout.
func PhaseOut(w io.Writer) io.Writer {
	if w == nil {
		return os.Stdout
	}
	return w
}

// EmitMarker writes a single exact marker line to w (resolved via PhaseOut).
func EmitMarker(w io.Writer, marker string) {
	fmt.Fprintln(PhaseOut(w), marker)
}
