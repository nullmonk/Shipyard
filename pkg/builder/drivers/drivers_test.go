package drivers_test

import (
	"testing"

	"shipyard/pkg/builder/drivers"
)

func TestGetDriver(t *testing.T) {
	tests := []struct {
		image    string
		expected string
	}{
		{"ubuntu:20.04", "*drivers.DebianDriver"},
		{"debian:bookworm", "*drivers.DebianDriver"},
		{"linuxmint:21", "*drivers.DebianDriver"},
		{"alpine:3.20", "*drivers.AlpineDriver"},
		{"rockylinux:9", "*drivers.RPMDriver"},
		{"centos:7", "*drivers.RPMDriver"},
		{"fedora:40", "*drivers.RPMDriver"},
		{"archlinux:base-devel", "*drivers.ArchDriver"},
		{"opensuse/leap:15.5", "*drivers.RPMDriver"},
	}

	for _, tt := range tests {
		driver, err := drivers.GetDriver(tt.image)
		if err != nil {
			t.Fatalf("unexpected error for %s: %v", tt.image, err)
		}
		if driver == nil {
			t.Fatalf("expected driver for %s, got nil", tt.image)
		}
	}

	_, err := drivers.GetDriver("windows:latest")
	if err == nil {
		t.Fatalf("expected error for unsupported image, got nil")
	}
}

