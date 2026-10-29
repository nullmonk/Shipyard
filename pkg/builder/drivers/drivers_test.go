package drivers_test

import (
	"strings"
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

func TestInstallArtifactsScript(t *testing.T) {
	tests := []struct {
		image    string
		dir      string
		commands []string
	}{
		{"ubuntu:22.04", "/tmp/build", []string{"apt-get install", "*.deb"}},
		{"debian:bookworm", "/tmp/build", []string{"apt-get install", "*.deb"}},
		{"fedora:40", "/tmp/build/RPMS", []string{"dnf install", "yum install", "*.rpm"}},
		{"rockylinux:9", "/tmp/build/RPMS", []string{"dnf install", "yum install", "*.rpm"}},
		{"centos:7", "/tmp/build/RPMS", []string{"dnf install", "yum install", "*.rpm"}},
		{"opensuse/leap:15.5", "/usr/src/packages/RPMS", []string{"zypper", "--allow-unsigned-rpm", "*.rpm"}},
		{"alpine:3.20", "/root/packages", []string{"apk add --allow-untrusted", "*.apk"}},
		{"archlinux:base-devel", "/tmp/build/artifacts", []string{"sudo pacman -U", "*.pkg.tar.zst"}},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			driver, err := drivers.GetDriver(tt.image)
			if err != nil {
				t.Fatalf("GetDriver(%s): %v", tt.image, err)
			}
			script := driver.InstallArtifactsScript()
			if script == "" {
				t.Fatalf("InstallArtifactsScript(%s) is empty", tt.image)
			}
			if !strings.Contains(script, tt.dir) {
				t.Errorf("script for %s should reference artifact dir %q, got:\n%s", tt.image, tt.dir, script)
			}
			for _, cmd := range tt.commands {
				if !strings.Contains(script, cmd) {
					t.Errorf("script for %s should contain %q, got:\n%s", tt.image, cmd, script)
				}
			}
			// Failing loudly on an empty artifact dir keeps tests from
			// silently running against uninstalled packages.
			if !strings.Contains(script, "exit 1") {
				t.Errorf("script for %s should exit non-zero when no artifacts are found, got:\n%s", tt.image, script)
			}
		})
	}
}
