package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"shipyard/pkg/shipfile"
	shipmod "shipyard/pkg/shipfile/modules/shipyard"
)

func testShipfileWithVars() *shipfile.Shipfile {
	return &shipfile.Shipfile{
		Variables: []shipmod.Variable{
			{Name: "PORT", Description: "Port", Value: "8080"},
			{Name: "ENV", Description: "Env", Value: "prod"},
		},
	}
}

func TestParseVarsInlineJSON(t *testing.T) {
	m, err := parseVarsValue(`{"PORT":"9090","ENV":"dev"}`)
	if err != nil {
		t.Fatalf("parseVarsValue: %v", err)
	}
	want := map[string]string{"PORT": "9090", "ENV": "dev"}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

func TestParseVarsCoercesScalars(t *testing.T) {
	m, err := parseVarsValue(`{"PORT":8080,"DEBUG":true,"NAME":"x"}`)
	if err != nil {
		t.Fatalf("parseVarsValue: %v", err)
	}
	want := map[string]string{"PORT": "8080", "DEBUG": "true", "NAME": "x"}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

func TestParseVarsRejectsNonObject(t *testing.T) {
	for _, raw := range []string{`[1,2]`, `"str"`, `42`, `not-json`, `{"A":{"nested":1}}`, `{"A":null}`, `{"A":[1]}`} {
		if _, err := parseVarsValue(raw); err == nil {
			t.Errorf("expected error for %q, got nil", raw)
		}
	}
}

func TestParseVarsFileForms(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vars.json")
	if err := os.WriteFile(p, []byte(`{"PORT":"1234"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// @-prefixed.
	m, err := parseVarsValue("@" + p)
	if err != nil {
		t.Fatalf("@file: %v", err)
	}
	if m["PORT"] != "1234" {
		t.Errorf("@file: got %v", m)
	}

	// Bare existing path auto-detected as file.
	m, err = parseVarsValue(p)
	if err != nil {
		t.Fatalf("bare path: %v", err)
	}
	if m["PORT"] != "1234" {
		t.Errorf("bare path: got %v", m)
	}

	// Missing file.
	if _, err := parseVarsValue("@" + filepath.Join(dir, "nope.json")); err == nil {
		t.Errorf("expected error for missing file, got nil")
	}
	// Empty @.
	if _, err := parseVarsValue("@"); err == nil {
		t.Errorf("expected error for bare @, got nil")
	}
}

func TestParseVarAssign(t *testing.T) {
	k, v, err := parseVarAssign("PORT=8080")
	if err != nil || k != "PORT" || v != "8080" {
		t.Errorf("got %q=%q err=%v", k, v, err)
	}
	// Value keeps '=' and spaces verbatim.
	k, v, err = parseVarAssign("OPTS=a=b c")
	if err != nil || k != "OPTS" || v != "a=b c" {
		t.Errorf("got %q=%q err=%v", k, v, err)
	}
	// Empty value allowed.
	k, v, err = parseVarAssign("EMPTY=")
	if err != nil || k != "EMPTY" || v != "" {
		t.Errorf("got %q=%q err=%v", k, v, err)
	}
	for _, bad := range []string{"NOEQUALS", "=x", "   =x"} {
		if _, _, err := parseVarAssign(bad); err == nil {
			t.Errorf("expected error for %q, got nil", bad)
		}
	}
}

func TestResolveVariableOverridesPrecedence(t *testing.T) {
	sf := testShipfileWithVars()
	m, err := resolveVariableOverrides(sf, `{"PORT":"1111","ENV":"a"}`, []string{"PORT=2222"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := map[string]string{"PORT": "2222", "ENV": "a"}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v (--var must win over --vars)", m, want)
	}
}

func TestResolveVariableOverridesUnknownWarnedAndDropped(t *testing.T) {
	sf := testShipfileWithVars()
	m, err := resolveVariableOverrides(sf, `{"PORT":"1111","BOGUS":"x"}`, []string{"ALSOBOGUS=y"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := map[string]string{"PORT": "1111"}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v (unknown keys must be dropped, not fail)", m, want)
	}
}

func TestResolveVariableOverridesEmpty(t *testing.T) {
	sf := testShipfileWithVars()
	m, err := resolveVariableOverrides(sf, "", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil map, got %v", m)
	}
}

func TestBuildCmdHasVarsFlags(t *testing.T) {
	// Reset package-level flag state from other tests.
	oldVars, oldVarFlags := buildVars, buildVarFlags
	t.Cleanup(func() { buildVars, buildVarFlags = oldVars, oldVarFlags })

	cmd := newBuildCmd()
	if err := cmd.ParseFlags([]string{`--vars`, `{"PORT":"1"}`, `--var`, `ENV=dev`, `--var`, `PORT=2`}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	v, _ := cmd.Flags().GetString("vars")
	if v != `{"PORT":"1"}` {
		t.Errorf("got vars=%q", v)
	}
	arr, _ := cmd.Flags().GetStringArray("var")
	if !reflect.DeepEqual(arr, []string{"ENV=dev", "PORT=2"}) {
		t.Errorf("got var=%v", arr)
	}
}
