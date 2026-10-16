package shipfile

// strext registers missing string methods into gpython's StringType.
// gpython's str only ships a small subset; this fills the gaps we need.

import (
	"fmt"
	"strings"

	"github.com/go-python/gpython/py"
)

func init() {
	d := py.StringType.Dict

	d["lower"] = py.MustNewMethod("lower", func(self py.Object, _ py.Tuple, _ py.StringDict) (py.Object, error) {
		s, ok := self.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "str.lower() requires a str")
		}
		return py.String(strings.ToLower(string(s))), nil
	}, 0, "lower() -> str")

	d["upper"] = py.MustNewMethod("upper", func(self py.Object, _ py.Tuple, _ py.StringDict) (py.Object, error) {
		s, ok := self.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "str.upper() requires a str")
		}
		return py.String(strings.ToUpper(string(s))), nil
	}, 0, "upper() -> str")

	d["join"] = py.MustNewMethod("join", func(self py.Object, args py.Tuple, _ py.StringDict) (py.Object, error) {
		sep, ok := self.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "str.join() requires a str separator")
		}
		if len(args) != 1 {
			return nil, py.ExceptionNewf(py.TypeError, "join() takes exactly one argument")
		}
		var parts []string
		switch v := args[0].(type) {
		case py.Tuple:
			for _, item := range v {
				s, ok := item.(py.String)
				if !ok {
					return nil, py.ExceptionNewf(py.TypeError, "sequence item must be str")
				}
				parts = append(parts, string(s))
			}
		case *py.List:
			for _, item := range v.Items {
				s, ok := item.(py.String)
				if !ok {
					return nil, py.ExceptionNewf(py.TypeError, "sequence item must be str")
				}
				parts = append(parts, string(s))
			}
		default:
			return nil, py.ExceptionNewf(py.TypeError, "join() argument must be iterable")
		}
		return py.String(strings.Join(parts, string(sep))), nil
	}, 0, "join(iterable) -> str")

	d["format"] = py.MustNewMethod("format", strFormat, 0, "format(*args, **kwargs) -> str")
}

// strFormat implements str.format(*args, **kwargs).
// Supports: {}, {n}, {name}, {!r}, {!s} — enough for validation messages.
func strFormat(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	tmpl, ok := self.(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "str.format() requires a str")
	}
	s := string(tmpl)
	var out strings.Builder
	autoIdx := 0

	i := 0
	for i < len(s) {
		if s[i] == '{' {
			if i+1 < len(s) && s[i+1] == '{' {
				out.WriteByte('{')
				i += 2
				continue
			}
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return nil, py.ExceptionNewf(py.ValueError, "Single '{' encountered in format string")
			}
			spec := s[i+1 : i+end]
			i += end + 1

			var val py.Object
			conv := ""

			// Split off conversion: {name!r} or {!r}
			if bang := strings.IndexByte(spec, '!'); bang >= 0 {
				conv = spec[bang+1:]
				spec = spec[:bang]
			}
			// Ignore format_spec after ':'
			if colon := strings.IndexByte(spec, ':'); colon >= 0 {
				spec = spec[:colon]
			}

			spec = strings.TrimSpace(spec)
			if spec == "" {
				if autoIdx >= len(args) {
					return nil, py.ExceptionNewf(py.IndexError, "Replacement index %d out of range", autoIdx)
				}
				val = args[autoIdx]
				autoIdx++
			} else if isDigits(spec) {
				idx := 0
				for _, c := range spec {
					idx = idx*10 + int(c-'0')
				}
				if idx >= len(args) {
					return nil, py.ExceptionNewf(py.IndexError, "Replacement index %d out of range", idx)
				}
				val = args[idx]
			} else {
				// keyword
				v, exists := kwargs[spec]
				if !exists {
					return nil, py.ExceptionNewf(py.KeyError, "KeyError: '%s'", spec)
				}
				val = v
			}

			switch conv {
			case "r":
				r, err := py.Repr(val)
				if err != nil {
					return nil, err
				}
				out.WriteString(string(r.(py.String)))
			default:
				out.WriteString(pyStr(val))
			}
			continue
		}
		if s[i] == '}' && i+1 < len(s) && s[i+1] == '}' {
			out.WriteByte('}')
			i += 2
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return py.String(out.String()), nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func pyStr(obj py.Object) string {
	if obj == nil || obj == py.None {
		return "None"
	}
	switch v := obj.(type) {
	case py.String:
		return string(v)
	case py.Int:
		return fmt.Sprintf("%d", int64(v))
	case py.Float:
		return fmt.Sprintf("%g", float64(v))
	case py.Bool:
		if v {
			return "True"
		}
		return "False"
	}
	if r, err := py.Repr(obj); err == nil {
		if s, ok := r.(py.String); ok {
			return string(s)
		}
	}
	return fmt.Sprintf("%v", obj)
}
