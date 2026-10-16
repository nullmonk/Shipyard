// Package remod registers the built-in "re" Python module backed by Go's regexp.
// Import it with _ to trigger registration:
//
//	import _ "shipyard/pkg/shipyard/modules/re"
package remod

import (
	"regexp"

	"github.com/go-python/gpython/py"
)

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "re",
			Doc:  "Regular expression operations (subset backed by Go regexp/RE2).",
		},
		Methods: []*py.Method{
			py.MustNewMethod("compile", reCompile, 0, "compile(pattern[, flags]) -> compiled pattern"),
			py.MustNewMethod("match", reMatchFn, 0, "match(pattern, string) -> match or None"),
			py.MustNewMethod("search", reSearchFn, 0, "search(pattern, string) -> match or None"),
			py.MustNewMethod("sub", reSubFn, 0, "sub(pattern, repl, string[, count]) -> string"),
			py.MustNewMethod("split", reSplitFn, 0, "split(pattern, string[, maxsplit]) -> list"),
			py.MustNewMethod("findall", reFindallFn, 0, "findall(pattern, string) -> list of strings"),
			py.MustNewMethod("escape", reEscapeFn, 0, "escape(pattern) -> string"),
		},
	})
}

// ── compiled pattern ──────────────────────────────────────────────────────────

var patternType = py.NewType("re.Pattern", "Compiled regular expression")

type rePattern struct {
	re      *regexp.Regexp
	pattern string
}

func (p *rePattern) Type() *py.Type { return patternType }

func (p *rePattern) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "pattern":
		return py.String(p.pattern), nil
	case "match":
		return py.MustNewMethod("match", func(_ py.Object, args py.Tuple) (py.Object, error) {
			s, err := singleStr(args, "match")
			if err != nil {
				return nil, err
			}
			return doMatch(p, s), nil
		}, 0, ""), nil
	case "search":
		return py.MustNewMethod("search", func(_ py.Object, args py.Tuple) (py.Object, error) {
			s, err := singleStr(args, "search")
			if err != nil {
				return nil, err
			}
			return doSearch(p, s), nil
		}, 0, ""), nil
	case "sub":
		return py.MustNewMethod("sub", func(_ py.Object, args py.Tuple) (py.Object, error) {
			return doSub(p, args)
		}, 0, ""), nil
	case "subn":
		return py.MustNewMethod("subn", func(_ py.Object, args py.Tuple) (py.Object, error) {
			return doSubn(p, args)
		}, 0, ""), nil
	case "split":
		return py.MustNewMethod("split", func(_ py.Object, args py.Tuple) (py.Object, error) {
			return doSplit(p, args)
		}, 0, ""), nil
	case "findall":
		return py.MustNewMethod("findall", func(_ py.Object, args py.Tuple) (py.Object, error) {
			s, err := singleStr(args, "findall")
			if err != nil {
				return nil, err
			}
			return doFindall(p, s), nil
		}, 0, ""), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'re.Pattern' object has no attribute '%s'", name)
}

// ── match object ──────────────────────────────────────────────────────────────

var matchType = py.NewType("re.Match", "Match object returned by re.match / re.search")

type reMatch struct {
	m   []int    // submatch indices (FindStringSubmatchIndex)
	nam []string // named subgroup names in order
	src string
}

func (m *reMatch) Type() *py.Type { return matchType }

func (m *reMatch) M__bool__() (py.Object, error) {
	if m == nil || m.m == nil {
		return py.False, nil
	}
	return py.True, nil
}

func (m *reMatch) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "group":
		return py.MustNewMethod("group", func(_ py.Object, args py.Tuple) (py.Object, error) {
			return m.group(args)
		}, 0, "group([index_or_name]) -> string"), nil
	case "groups":
		return py.MustNewMethod("groups", func(_ py.Object, _ py.Tuple) (py.Object, error) {
			return m.groups()
		}, 0, "groups() -> tuple"), nil
	case "start":
		return py.MustNewMethod("start", func(_ py.Object, args py.Tuple) (py.Object, error) {
			idx, err := optInt(args, 0)
			if err != nil {
				return nil, err
			}
			s, _ := m.span(idx)
			return py.Int(s), nil
		}, 0, ""), nil
	case "end":
		return py.MustNewMethod("end", func(_ py.Object, args py.Tuple) (py.Object, error) {
			idx, err := optInt(args, 0)
			if err != nil {
				return nil, err
			}
			_, e := m.span(idx)
			return py.Int(e), nil
		}, 0, ""), nil
	case "span":
		return py.MustNewMethod("span", func(_ py.Object, args py.Tuple) (py.Object, error) {
			idx, err := optInt(args, 0)
			if err != nil {
				return nil, err
			}
			s, e := m.span(idx)
			return py.Tuple{py.Int(s), py.Int(e)}, nil
		}, 0, ""), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'re.Match' object has no attribute '%s'", name)
}

func (m *reMatch) span(group int) (int, int) {
	i := group * 2
	if i+1 >= len(m.m) {
		return -1, -1
	}
	return m.m[i], m.m[i+1]
}

func (m *reMatch) group(args py.Tuple) (py.Object, error) {
	if len(args) == 0 {
		s, e := m.span(0)
		if s < 0 {
			return py.None, nil
		}
		return py.String(m.src[s:e]), nil
	}
	switch v := args[0].(type) {
	case py.Int:
		s, e := m.span(int(v))
		if s < 0 {
			return py.None, nil
		}
		return py.String(m.src[s:e]), nil
	case py.String:
		name := string(v)
		for i, n := range m.nam {
			if n == name {
				s, e := m.span(i + 1)
				if s < 0 {
					return py.None, nil
				}
				return py.String(m.src[s:e]), nil
			}
		}
		return nil, py.ExceptionNewf(py.IndexError, "no such group: %s", name)
	}
	return nil, py.ExceptionNewf(py.TypeError, "group index must be int or string")
}

func (m *reMatch) groups() (py.Object, error) {
	n := len(m.m)/2 - 1
	t := make(py.Tuple, n)
	for i := 0; i < n; i++ {
		s, e := m.span(i + 1)
		if s < 0 {
			t[i] = py.None
		} else {
			t[i] = py.String(m.src[s:e])
		}
	}
	return t, nil
}

// ── pattern helpers ───────────────────────────────────────────────────────────

func compile(pattern string) (*rePattern, error) {
	r, err := regexp.Compile(pattern)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "invalid regex %q: %v", pattern, err)
	}
	return &rePattern{re: r, pattern: pattern}, nil
}

func makeMatch(p *rePattern, src string, idx []int) py.Object {
	if idx == nil {
		return py.None
	}
	return &reMatch{m: idx, nam: p.re.SubexpNames()[1:], src: src}
}

func doMatch(p *rePattern, s string) py.Object {
	return makeMatch(p, s, p.re.FindStringSubmatchIndex(s))
}

func doSearch(p *rePattern, s string) py.Object {
	return makeMatch(p, s, p.re.FindStringSubmatchIndex(s))
}

func doSub(p *rePattern, args py.Tuple) (py.Object, error) {
	r, _, err := sub(p, args)
	return r, err
}

func doSubn(p *rePattern, args py.Tuple) (py.Object, error) {
	r, n, err := sub(p, args)
	if err != nil {
		return nil, err
	}
	return py.Tuple{r, py.Int(n)}, nil
}

func sub(p *rePattern, args py.Tuple) (py.Object, int, error) {
	if len(args) < 2 {
		return nil, 0, py.ExceptionNewf(py.TypeError, "sub() requires repl and string")
	}
	repl, ok1 := args[0].(py.String)
	src, ok2 := args[1].(py.String)
	if !ok1 || !ok2 {
		return nil, 0, py.ExceptionNewf(py.TypeError, "sub() repl and string must be strings")
	}
	count := -1
	if len(args) >= 3 {
		n, ok := args[2].(py.Int)
		if !ok {
			return nil, 0, py.ExceptionNewf(py.TypeError, "sub() count must be an int")
		}
		if int(n) > 0 {
			count = int(n)
		}
	}
	result := string(src)
	if count > 0 {
		c := 0
		result = p.re.ReplaceAllStringFunc(result, func(m string) string {
			if c >= count {
				return m
			}
			c++
			return string(repl)
		})
	} else {
		result = p.re.ReplaceAllLiteralString(result, string(repl))
	}
	matches := p.re.FindAllString(string(src), -1)
	return py.String(result), len(matches), nil
}

func doSplit(p *rePattern, args py.Tuple) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "split() requires a string argument")
	}
	src, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "split() string must be a string")
	}
	maxsplit := -1
	if len(args) >= 2 {
		n, ok := args[1].(py.Int)
		if ok && int(n) > 0 {
			maxsplit = int(n) + 1
		}
	}
	parts := p.re.Split(string(src), maxsplit)
	items := make([]py.Object, len(parts))
	for i, s := range parts {
		items[i] = py.String(s)
	}
	return &py.List{Items: items}, nil
}

func doFindall(p *rePattern, s string) py.Object {
	matches := p.re.FindAllString(s, -1)
	items := make([]py.Object, len(matches))
	for i, m := range matches {
		items[i] = py.String(m)
	}
	return &py.List{Items: items}
}

// ── module-level functions ────────────────────────────────────────────────────

func reCompile(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "compile() requires a pattern argument")
	}
	pat, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "compile() pattern must be a string")
	}
	return compile(string(pat))
}

func reMatchFn(self py.Object, args py.Tuple) (py.Object, error) {
	pat, src, err := twoStr(args, "match")
	if err != nil {
		return nil, err
	}
	p, err := compile(pat)
	if err != nil {
		return nil, err
	}
	return doMatch(p, src), nil
}

func reSearchFn(self py.Object, args py.Tuple) (py.Object, error) {
	pat, src, err := twoStr(args, "search")
	if err != nil {
		return nil, err
	}
	p, err := compile(pat)
	if err != nil {
		return nil, err
	}
	return doSearch(p, src), nil
}

func reSubFn(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) < 3 {
		return nil, py.ExceptionNewf(py.TypeError, "sub() requires pattern, repl, and string")
	}
	pat, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "sub() pattern must be a string")
	}
	p, err := compile(string(pat))
	if err != nil {
		return nil, err
	}
	return doSub(p, args[1:])
}

func reSplitFn(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) < 2 {
		return nil, py.ExceptionNewf(py.TypeError, "split() requires pattern and string")
	}
	pat, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "split() pattern must be a string")
	}
	p, err := compile(string(pat))
	if err != nil {
		return nil, err
	}
	return doSplit(p, args[1:])
}

func reFindallFn(self py.Object, args py.Tuple) (py.Object, error) {
	pat, src, err := twoStr(args, "findall")
	if err != nil {
		return nil, err
	}
	p, err := compile(pat)
	if err != nil {
		return nil, err
	}
	return doFindall(p, src), nil
}

func reEscapeFn(self py.Object, args py.Tuple) (py.Object, error) {
	s, err := singleStr(args, "escape")
	if err != nil {
		return nil, err
	}
	return py.String(regexp.QuoteMeta(s)), nil
}

// ── arg helpers ───────────────────────────────────────────────────────────────

func singleStr(args py.Tuple, name string) (string, error) {
	if len(args) < 1 {
		return "", py.ExceptionNewf(py.TypeError, "%s() requires a string argument", name)
	}
	s, ok := args[0].(py.String)
	if !ok {
		return "", py.ExceptionNewf(py.TypeError, "%s() argument must be a string", name)
	}
	return string(s), nil
}

func twoStr(args py.Tuple, name string) (string, string, error) {
	if len(args) < 2 {
		return "", "", py.ExceptionNewf(py.TypeError, "%s() requires two string arguments", name)
	}
	a, ok1 := args[0].(py.String)
	b, ok2 := args[1].(py.String)
	if !ok1 || !ok2 {
		return "", "", py.ExceptionNewf(py.TypeError, "%s() arguments must be strings", name)
	}
	return string(a), string(b), nil
}

func optInt(args py.Tuple, def int) (int, error) {
	if len(args) == 0 {
		return def, nil
	}
	n, ok := args[0].(py.Int)
	if !ok {
		return 0, py.ExceptionNewf(py.TypeError, "group index must be an int")
	}
	return int(n), nil
}
