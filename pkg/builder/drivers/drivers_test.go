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

func TestInstallPackageScript(t *testing.T) {
	tests := []struct {
		image string
		// fileTarget exercises the package-file branch, nameTarget the repo branch.
		fileTarget string
		nameTarget string
		fileCmds   []string
		repoCmds   []string
	}{
		{
			"ubuntu:22.04", "/tmp/build/openssh-server_8.9p1.deb", "openssh-server",
			[]string{"*.deb)", `f="./$t"`, `apt-get install -y --allow-downgrades "$f"`},
			[]string{`apt-get install -y "$t"`},
		},
		{
			"debian:bookworm", "./pkgs/foo.deb", "curl",
			[]string{"*.deb)", `f="$t"`, `apt-get install -y --allow-downgrades "$f"`},
			[]string{`apt-get install -y "$t"`},
		},
		{
			"fedora:40", "/tmp/build/RPMS/x86_64/openssh-9.8p1.rpm", "openssh-server",
			[]string{"*.rpm)", `f="./$t"`, `dnf install -y "$f"`, `yum install -y "$f"`},
			[]string{`dnf install -y "$t"`, `yum install -y "$t"`},
		},
		{
			"rockylinux:9", "./openssh-9.8p1.rpm", "vim",
			[]string{"*.rpm)", `f="$t"`, `dnf install -y "$f"`},
			[]string{`dnf install -y "$t"`},
		},
		{
			"centos:7", "/tmp/x.rpm", "git",
			[]string{"*.rpm)", `dnf install -y "$f"`, `yum install -y "$f"`},
			[]string{`yum install -y "$t"`},
		},
		{
			"opensuse/leap:15.5", "/usr/src/packages/RPMS/x86_64/foo.rpm", "vim",
			[]string{"*.rpm)", `zypper --non-interactive install --allow-unsigned-rpm "$t"`},
			[]string{`zypper --non-interactive install "$t"`},
		},
		{
			"alpine:3.20", "/root/packages/foo-1.0.apk", "curl",
			[]string{"*.apk)", `apk add --allow-untrusted "$t"`},
			[]string{`apk add "$t"`},
		},
		{
			"archlinux:base-devel", "/tmp/build/artifacts/foo-1.0.pkg.tar.zst", "openssh",
			[]string{"*.pkg.tar.*)", `pacman -U --noconfirm --needed "$t"`},
			[]string{`pacman -S --noconfirm --needed "$t"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.image+"/file", func(t *testing.T) {
			driver, err := drivers.GetDriver(tt.image)
			if err != nil {
				t.Fatalf("GetDriver(%s): %v", tt.image, err)
			}
			script := driver.InstallPackageScript(tt.fileTarget)
			if script == "" {
				t.Fatalf("InstallPackageScript(%s, file) is empty", tt.image)
			}
			if !strings.Contains(script, "t='"+tt.fileTarget+"'") {
				t.Errorf("script for %s should bind t to %q, got:\n%s", tt.image, tt.fileTarget, script)
			}
			for _, cmd := range tt.fileCmds {
				if !strings.Contains(script, cmd) {
					t.Errorf("file script for %s should contain %q, got:\n%s", tt.image, cmd, script)
				}
			}
			if !strings.Contains(script, "exit") {
				t.Errorf("file script for %s should exit non-zero on failure, got:\n%s", tt.image, script)
			}
		})
		t.Run(tt.image+"/name", func(t *testing.T) {
			driver, err := drivers.GetDriver(tt.image)
			if err != nil {
				t.Fatalf("GetDriver(%s): %v", tt.image, err)
			}
			script := driver.InstallPackageScript(tt.nameTarget)
			if script == "" {
				t.Fatalf("InstallPackageScript(%s, name) is empty", tt.image)
			}
			if !strings.Contains(script, "t='"+tt.nameTarget+"'") {
				t.Errorf("script for %s should bind t to %q, got:\n%s", tt.image, tt.nameTarget, script)
			}
			for _, cmd := range tt.repoCmds {
				if !strings.Contains(script, cmd) {
					t.Errorf("repo script for %s should contain %q, got:\n%s", tt.image, cmd, script)
				}
			}
			if !strings.Contains(script, "exit") {
				t.Errorf("repo script for %s should exit non-zero on failure, got:\n%s", tt.image, script)
			}
		})
	}
}

func TestInstallPackageScriptQuoting(t *testing.T) {
	driver, err := drivers.GetDriver("debian:bookworm")
	if err != nil {
		t.Fatalf("GetDriver: %v", err)
	}
	script := driver.InstallPackageScript("o'brien/foo bar.deb")
	if !strings.Contains(script, `t='o'"'"'brien/foo bar.deb'`) {
		t.Errorf("target should be single-quote escaped, got:\n%s", script)
	}
	// The quoted target must still drive the file branch via suffix match.
	if !strings.Contains(script, `apt-get install -y --allow-downgrades "$f"`) {
		t.Errorf("quoted file target should still use the file-install command, got:\n%s", script)
	}
}
