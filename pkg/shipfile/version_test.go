package shipfile_test

import (
	"testing"

	"github.com/go-python/gpython/py"
	"shipyard/pkg/shipfile"
	shipmod "shipyard/pkg/shipfile/modules/shipyard"
)

// TestVersionOperatorsInGpython exercises the shipyard.Version type inside
// the gpython interpreter: mixed-type comparisons in both directions,
// string-like behaviour, and the missing-parts-are-zero semantics.
func TestVersionOperatorsInGpython(t *testing.T) {
	interp := newTestInterpreter(t)
	src := `
from shipyard import Version

v = Version("9.4p2")

# direct mixed-type comparisons
r_lt_float = (v < 9.1)        # 9.4p2 > 9.1, so False
r_gt_float = (v > 9.1)        # True
r_gt_int = (v > 8)            # True
r_lt_int = (v < 10)           # True
r_eq_str = (v == "9.4.2")     # same digit runs, True
r_ne_str = (v != "9.4.3")     # True
r_ge = (v >= "2.4.0")         # True (existing shipfile idiom)
r_le = (v <= "9.4.2")         # True

# reflected: builtin on the left, Version on the right
r_rev_lt = (9.1 < v)          # True, via v.__gt__
r_rev_gt = (9.1 > v)          # False, via v.__lt__
r_rev_int = (8 < v)           # True
r_rev_int2 = (10 > v)         # True
r_rev_eq = ("9.4.2" == v)     # True
r_rev_ne = ("9.4.3" != v)     # True

# string-like behaviour
r_str = str(v)                # "9.4p2"
r_repr = repr(v)              # "Version('9.4p2')"
r_isinst = isinstance(v, Version)

# constructor coercions
r_from_float = str(Version(9.1))   # "9.1"
r_from_int = str(Version(8))       # "8"
r_from_empty = str(Version())      # ""
r_rewrap = (Version(v) == v)       # True

# missing parts count as 0
r_pad_eq = (Version("9.4") == "9.4.0")     # True
r_pad_ge = (Version("9.4.0") >= "9.4")     # True
r_pad_lt = (Version("9.4") < "9.4.1")      # True

# non-numeric junk is ignored, like the old _vparts helpers
r_junk = (Version("openssh-7.9p1") == "7.9.1")  # True

# membership tests route through ==, including reflected
r_in = (v in ["1.0", "9.4.2"])       # True
r_not_in = ("0.0" in [v])            # False

# Context wraps plain strings automatically
from shipyard import Context
ctx = Context(version="9.9p2")
r_ctx_isinst = isinstance(ctx.version, Version)
r_ctx_str = str(ctx.version)
r_ctx_ge = (ctx.version >= "2.4.0")
`
	mod, err := runPySrc(t, interp, src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	wantBool := map[string]py.Object{
		"r_lt_float": py.False,
		"r_gt_float": py.True,
		"r_gt_int":   py.True,
		"r_lt_int":   py.True,
		"r_eq_str":   py.True,
		"r_ne_str":   py.True,
		"r_ge":       py.True,
		"r_le":       py.True,
		"r_rev_lt":   py.True,
		"r_rev_gt":   py.False,
		"r_rev_int":  py.True,
		"r_rev_int2": py.True,
		"r_rev_eq":   py.True,
		"r_rev_ne":   py.True,
		"r_isinst":   py.True,
		"r_rewrap":   py.True,
		"r_pad_eq":   py.True,
		"r_pad_ge":   py.True,
		"r_pad_lt":   py.True,
		"r_junk":     py.True,
		"r_in":       py.True,
		"r_not_in":   py.False,
		"r_ctx_isinst": py.True,
		"r_ctx_ge":     py.True,
	}
	for name, want := range wantBool {
		got, ok := mod.Globals[name]
		if !ok {
			t.Errorf("global %q missing", name)
			continue
		}
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}

	wantStr := map[string]string{
		"r_str":        "9.4p2",
		"r_repr":       "Version('9.4p2')",
		"r_from_float": "9.1",
		"r_from_int":   "8",
		"r_from_empty": "",
		"r_ctx_str":    "9.9p2",
	}
	for name, want := range wantStr {
		got, ok := mod.Globals[name]
		if !ok {
			t.Errorf("global %q missing", name)
			continue
		}
		s, ok := got.(py.String)
		if !ok {
			t.Errorf("%s is %s, want str", name, got.Type().Name)
			continue
		}
		if string(s) != want {
			t.Errorf("%s = %q, want %q", name, string(s), want)
		}
	}
}

// TestNewContextVersionType verifies that Shipfile.NewContext (the Go path
// used by CallMethod, RunCodePatches, RunTests, Setup and Build) produces a
// ctx whose version is a shipyard.Version holding the original string.
func TestNewContextVersionType(t *testing.T) {
	interp := newTestInterpreter(t)
	sf, err := shipfile.ValidateString(`from shipyard import Builder
class Shipfile(Builder):
    Name = "ver-ctx"
    def setup(self): pass
`, interp)
	if err != nil {
		t.Fatalf("ValidateString: %v", err)
	}

	ctxObj, err := sf.NewContext("9.9p2")
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	verObj, err := py.GetAttrString(ctxObj, "version")
	if err != nil {
		t.Fatalf("get ctx.version: %v", err)
	}
	v, ok := verObj.(*shipmod.Version)
	if !ok {
		t.Fatalf("ctx.version is %s, want *shipmod.Version", verObj.Type().Name)
	}
	if v.S != "9.9p2" {
		t.Errorf("ctx.version = %q, want %q", v.S, "9.9p2")
	}
}

// TestCtxVersionEndToEnd runs a shipfile test through RunTests (which builds
// the Context from RunContext.Version) asserting Version semantics on
// self.ctx.version inside gpython.
func TestCtxVersionEndToEnd(t *testing.T) {
	interp := newTestInterpreter(t)
	sf, err := shipfile.ValidateString(`from shipyard import Builder, Tester, Version, test
class Shipfile(Builder, Tester):
    Name = "ver-e2e"
    Url = "https://github.com/example/repo"

    def build(self): pass
    def save(self): pass

    @test("ctx.version comparisons")
    def check_version(self):
        v = self.ctx.version
        assert isinstance(v, Version)
        assert str(v) == "9.9p2"
        assert v >= "2.4.0"
        assert v > 8
        assert v < 10
        assert v == "9.9.2"
        assert 8 < v
        assert 9.1 < v
        assert "9.9.2" == v
        assert Version("9.4") == "9.4.0"
`, interp)
	if err != nil {
		t.Fatalf("ValidateString: %v", err)
	}

	results, err := sf.RunTests(shipfile.RunContext{Version: "9.9p2"})
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("expected 1 passing test, got %+v", results)
	}
}

// TestVersionGoDispatch checks the Go-level operator dispatch (the same
// py.Lt/py.Eq entry points the gpython VM uses) and the equality/hash
// contract: equal versions compare equal and hash equal.
func TestVersionGoDispatch(t *testing.T) {
	mk := func(s string) *shipmod.Version { return &shipmod.Version{S: s} }

	cases := []struct {
		name string
		fn   func() (py.Object, error)
		want py.Object
	}{
		{"lt float", func() (py.Object, error) { return py.Lt(mk("9.4p2"), py.Float(9.1)) }, py.False},
		{"gt float", func() (py.Object, error) { return py.Gt(mk("9.4p2"), py.Float(9.1)) }, py.True},
		{"gt int", func() (py.Object, error) { return py.Gt(mk("9.4p2"), py.Int(8)) }, py.True},
		{"eq str", func() (py.Object, error) { return py.Eq(mk("9.4p2"), py.String("9.4.2")) }, py.True},
		{"rev lt", func() (py.Object, error) { return py.Lt(py.Float(9.1), mk("9.4p2")) }, py.True},
		{"rev eq", func() (py.Object, error) { return py.Eq(py.String("9.4.2"), mk("9.4p2")) }, py.True},
		{"pad eq", func() (py.Object, error) { return py.Eq(mk("9.4"), py.String("9.4.0")) }, py.True},
		{"ge", func() (py.Object, error) { return py.Ge(mk("9.9p2"), py.String("2.4.0")) }, py.True},
	}
	for _, c := range cases {
		got, err := c.fn()
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}

	hashOf := func(s string) py.Object {
		v := mk(s)
		h, err := v.M__hash__()
		if err != nil {
			t.Fatalf("hash(%q): %v", s, err)
		}
		return h
	}
	if hashOf("9.4p2") != hashOf("9.4.2") {
		t.Errorf("equal versions hash differently")
	}
	if hashOf("9.4") != hashOf("9.4.0") {
		t.Errorf("zero-padded equal versions hash differently")
	}
	if hashOf("9.4") == hashOf("9.5") {
		t.Errorf("different versions hash the same")
	}
}
