package shipfile_test

import (
	"path/filepath"
	"testing"

	"shipyard/pkg/shipfile"
)

func TestLoadOpensshShipfile(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	sfPath := filepath.Join(root, "examples/openssh/shipfile.py")
	sf, err := shipfile.LoadDefault(sfPath, root)
	if err != nil {
		t.Fatalf("failed to load shipfile: %v", err)
	}

	if sf.Name != "openssh" {
		t.Errorf("expected Name 'openssh', got %q", sf.Name)
	}
	if sf.Url != "https://github.com/openssh/openssh-portable" {
		t.Errorf("expected Url 'https://github.com/openssh/openssh-portable', got %q", sf.Url)
	}
	if sf.PackageName != "openssh" {
		t.Errorf("expected PackageName 'openssh', got %q", sf.PackageName)
	}
	if sf.Variables["__BANNER__"] != "opensource is good" {
		t.Errorf("expected __BANNER__ variable 'opensource is good', got %q", sf.Variables["__BANNER__"])
	}

	if !sf.IsPatch() {
		t.Errorf("expected sf.IsPatch() == true for openssh (subclasses Patcher)")
	}
	if sf.IsBuild() {
		t.Errorf("expected sf.IsBuild() == false for openssh (does not subclass Builder)")
	}
	if !sf.HasCodePatches {
		t.Errorf("expected sf.HasCodePatches == true for openssh")
	}
	if err := sf.Validate(); err != nil {
		t.Errorf("expected openssh shipfile to validate, got error: %v", err)
	}
}

func TestLoadHelloShipfile(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	sfPath := filepath.Join(root, "examples/hello/shipfile.py")
	sf, err := shipfile.LoadDefault(sfPath, root)
	if err != nil {
		t.Fatalf("failed to load shipfile: %v", err)
	}

	if sf.Name != "hello" {
		t.Errorf("expected Name 'hello', got %q", sf.Name)
	}
	if !sf.IsBuild() {
		t.Errorf("expected sf.IsBuild() == true for hello (subclasses Builder)")
	}
	if sf.IsPatch() {
		t.Errorf("expected sf.IsPatch() == false for hello (does not subclass Patcher)")
	}
	if err := sf.Validate(); err != nil {
		t.Errorf("expected hello shipfile to validate, got error: %v", err)
	}
}
