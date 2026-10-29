package shipmod

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-python/gpython/py"
)

// versionType is the Python-visible "Version" type exported by the shipyard
// module, so shipfiles can do `from shipyard import Version` and call
// `Version("9.4p2")` directly.
//
// It is a Go-backed type rather than a pure-Python class because the gpython
// interpreter only dispatches rich comparison operators (<, <=, ==, !=, >,
// >=) to Go-implemented M__lt__/M__le__/M__eq__/M__ne__/M__gt__/M__ge__
// methods. Dunders defined on pure-Python classes are never consulted by the
// gpython VM for binary operators: `C(1) < C(2)` raises TypeError and
// `C(1) == C(1)` falls back to identity. A Go type gets full operator
// support, including reflected dispatch (`9.1 > v`, `"9.4.2" == v`) when the
// left operand's own comparison returns NotImplemented.
//
// Comparison semantics: both sides are reduced to numeric-part lists by
// splitting on non-digit runs and dropping empties (so "openssh-7.9p1" and
// "7.9.1" both become [7 9 1]), then compared element-wise with missing
// parts treated as 0. Floats convert via their str() rendering, so
// 9.1 -> [9 1].
var versionType = py.NewTypeX("Version", versionDoc, versionNew, versionInit)

const versionDoc = `Version(s) -> version with numeric-part comparison

String-like: str(v) returns the original string. Versions compare
element-wise on digit runs with missing parts treated as 0, so
Version("9.4p2") == "9.4.2" and Version("9.4") == "9.4.0".
Mixed-type comparisons work: Version("9.4p2") > 8 and
Version("9.4p2") > 9.1, including reflected forms (8 < v, 9.1 < v).`

func init() {
	if err := versionType.Ready(); err != nil {
		panic(fmt.Sprintf("shipyard: cannot ready Version type: %v", err))
	}
}

// Version is a string-like version value for shipfiles. The zero value is
// the empty version. Construct via versionNew/versionInit (i.e. from Python
// as Version("9.4p2")); the S field holds the original string.
type Version struct {
	S string
}

// Type returns the Python type object (shipyard.Version).
func (v *Version) Type() *py.Type { return versionType }

// String returns the original version string. Besides debugging, this makes
// Go's fmt ("%s" % v style formatting in shipfiles goes through fmt)
// render the version instead of a Go struct dump.
func (v *Version) String() string { return v.S }

// M__str__ implements str(v), returning the original string.
func (v *Version) M__str__() (py.Object, error) { return py.String(v.S), nil }

// M__repr__ implements repr(v) as Version('9.4p2').
func (v *Version) M__repr__() (py.Object, error) {
	return py.String("Version(" + pythonRepr(v.S) + ")"), nil
}

// M__lt__ implements v < other.
func (v *Version) M__lt__(other py.Object) (py.Object, error) {
	return py.NewBool(v.compare(other) < 0), nil
}

// M__le__ implements v <= other.
func (v *Version) M__le__(other py.Object) (py.Object, error) {
	return py.NewBool(v.compare(other) <= 0), nil
}

// M__eq__ implements v == other.
func (v *Version) M__eq__(other py.Object) (py.Object, error) {
	return py.NewBool(v.compare(other) == 0), nil
}

// M__ne__ implements v != other.
func (v *Version) M__ne__(other py.Object) (py.Object, error) {
	return py.NewBool(v.compare(other) != 0), nil
}

// M__gt__ implements v > other.
func (v *Version) M__gt__(other py.Object) (py.Object, error) {
	return py.NewBool(v.compare(other) > 0), nil
}

// M__ge__ implements v >= other.
func (v *Version) M__ge__(other py.Object) (py.Object, error) {
	return py.NewBool(v.compare(other) >= 0), nil
}

// M__hash__ implements hash(v). gpython has no hash() builtin and its dicts
// are string-keyed, so this is only reachable from Go, but it upholds the
// equality/hash contract: equal versions hash equal. Trailing zero parts
// are stripped first so Version("9.4") and Version("9.4.0") agree.
func (v *Version) M__hash__() (py.Object, error) {
	parts := versionParts(v.S)
	for len(parts) > 0 && parts[len(parts)-1] == 0 {
		parts = parts[:len(parts)-1]
	}
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for _, p := range parts {
		h ^= uint64(int64(p))
		h *= prime64
	}
	return py.Int(int64(h)), nil
}

// compare orders v against other (negative/zero/positive). The other side
// is coerced with versionStringOf, so str, int, float, bool and Version all
// work, and unknown objects fall back to their str() rendering.
func (v *Version) compare(other py.Object) int {
	return compareVersionParts(versionParts(v.S), versionParts(versionStringOf(other)))
}

// versionNew allocates an empty Version; versionInit fills it in.
func versionNew(metatype *py.Type, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	return &Version{}, nil
}

// versionInit implements Version(s=""). Only basic syntax is used anywhere
// in this file, but this runs as Go code, not in gpython, so that
// constraint does not apply here.
func versionInit(self py.Object, args py.Tuple, kwargs py.StringDict) error {
	v, ok := self.(*Version)
	if !ok {
		return py.ExceptionNewf(py.TypeError, "Version.__init__: self is %s, not Version", self.Type().Name)
	}
	if len(kwargs) > 0 {
		return py.ExceptionNewf(py.TypeError, "Version() takes no keyword arguments")
	}
	if len(args) > 1 {
		return py.ExceptionNewf(py.TypeError, "Version() takes at most 1 argument (%d given)", len(args))
	}
	if len(args) == 0 {
		v.S = ""
		return nil
	}
	v.S = versionStringOf(args[0])
	return nil
}

// versionStringOf renders a comparison operand the way the Version
// constructor does: Versions unwrap, strings pass through, bools become
// "1"/"0", ints and floats use gpython's own str() rendering (so 9.1 ->
// "9.1"), None becomes "", and anything else falls back to str(obj),
// mirroring the old pure-Python Version(str(o)) coercion.
func versionStringOf(obj py.Object) string {
	if obj == nil || obj == py.None {
		return ""
	}
	switch t := obj.(type) {
	case *Version:
		return t.S
	case py.String:
		return string(t)
	case py.Bool:
		if bool(t) {
			return "1"
		}
		return "0"
	default:
		s, err := py.Str(obj)
		if err != nil {
			return ""
		}
		if str, ok := s.(py.String); ok {
			return string(str)
		}
		return ""
	}
}

// versionParts splits s on non-digit runs and drops empties, returning each
// digit run as an int. "openssh-7.9p1" and "7.9.1" both yield [7 9 1];
// non-numeric junk contributes nothing.
func versionParts(s string) []int {
	var parts []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		if n, err := strconv.Atoi(f); err == nil {
			parts = append(parts, n)
		}
	}
	return parts
}

// compareVersionParts orders two part lists element-wise with missing parts
// treated as 0, so [9 4] equals [9 4 0] and [9 4] sorts below [9 4 1].
func compareVersionParts(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// pythonRepr quotes s Python-style with single quotes.
func pythonRepr(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '\'':
			b.WriteString("\\'")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
