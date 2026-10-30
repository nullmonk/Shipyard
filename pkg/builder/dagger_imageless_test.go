package builder

import (
	"bytes"
	"strings"
	"testing"
)

// Imageless fail-fast guard: engine-free coverage. From() tolerates a nil
// client so hasImage tracking can be exercised without a Dagger engine;
// full container exec is covered by the existing engine-gated tests.

func TestDaggerBuilderImagelessRunFailsFast(t *testing.T) {
	var out bytes.Buffer
	db := &DaggerBuilder{out: &out, cmdOut: &out, cmdErr: &out}
	_, _, _, err := db.Run([]string{"/bin/sh", "-c", "echo hi"}, "")
	if err == nil {
		t.Fatal("expected imageless Run to fail, got nil error")
	}
	if !strings.Contains(err.Error(), "no base image selected") {
		t.Fatalf("expected 'no base image selected' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "From() before Run()") {
		t.Fatalf("expected error to mention From() before Run(), got: %v", err)
	}
}

func TestDaggerBuilderSeededImageGuardPasses(t *testing.T) {
	var out bytes.Buffer
	// Simulates BuildShipfile seeding from --image/BaseImage (hasImage=true).
	db := &DaggerBuilder{out: &out, cmdOut: &out, cmdErr: &out, hasImage: true}
	if err := db.requireImage("Run"); err != nil {
		t.Fatalf("seeded builder should pass image guard, got: %v", err)
	}
	// Run proceeds past the imageless guard (then hits the nil-container
	// backstop since no engine/container exists in this unit test). The key
	// assertion: it must NOT report the imageless error.
	_, _, _, err := db.Run([]string{"echo", "hi"}, "")
	if err != nil && strings.Contains(err.Error(), "no base image selected") {
		t.Fatalf("seeded builder Run must not fail with imageless error, got: %v", err)
	}
}

func TestDaggerBuilderFromThenRunGuardPasses(t *testing.T) {
	var out bytes.Buffer
	db := &DaggerBuilder{out: &out, cmdOut: &out, cmdErr: &out}
	if err := db.From("alpine:3.19", ""); err != nil {
		t.Fatalf("From failed: %v", err)
	}
	if !db.hasImage {
		t.Fatal("expected hasImage=true after From()")
	}
	if err := db.requireImage("Run"); err != nil {
		t.Fatalf("From()-seeded builder should pass image guard, got: %v", err)
	}
}

func TestDaggerBuilderNamedStageFromGuardPasses(t *testing.T) {
	var out bytes.Buffer
	db := &DaggerBuilder{out: &out, cmdOut: &out, cmdErr: &out}
	if err := db.From("alpine:3.19", "builder"); err != nil {
		t.Fatalf("From(builder) failed: %v", err)
	}
	if err := db.From("alpine:3.19", "final"); err != nil {
		t.Fatalf("From(final) failed: %v", err)
	}
	if !db.hasImage {
		t.Fatal("expected hasImage=true after named-stage From() calls")
	}
	if err := db.Use("builder"); err != nil {
		t.Fatalf("Use(builder) failed: %v", err)
	}
	if err := db.requireImage("Run"); err != nil {
		t.Fatalf("named-stage builder should pass image guard after Use(), got: %v", err)
	}
	if err := db.Use("final"); err != nil {
		t.Fatalf("Use(final) failed: %v", err)
	}
	if err := db.requireImage("Run"); err != nil {
		t.Fatalf("named-stage builder should pass image guard, got: %v", err)
	}
}
