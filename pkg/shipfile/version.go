package shipfile

import (
	"fmt"
	"regexp"
	"strconv"
)

var nonDigit = regexp.MustCompile(`[^\d]+`)

// Version is a string that compares semantically by numeric components.
// "openssh-7.9p1" and "7.9.1" are considered equal — only digit runs matter.
// This mirrors the behaviour of the Python Version class in shipyard/version.py.
type Version string

func (v Version) parts() []int {
	raw := nonDigit.Split(string(v), -1)
	out := make([]int, 0, len(raw))
	for _, s := range raw {
		if s == "" {
			continue
		}
		if n, err := strconv.Atoi(s); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func cmp(a, b Version) int {
	ap, bp := a.parts(), b.parts()
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	if len(ap) < len(bp) {
		return -1
	}
	if len(ap) > len(bp) {
		return 1
	}
	return 0
}

func (v Version) Less(other Version) bool      { return cmp(v, other) < 0 }
func (v Version) Greater(other Version) bool   { return cmp(v, other) > 0 }
func (v Version) Equal(other Version) bool     { return cmp(v, other) == 0 }
func (v Version) LessEq(other Version) bool    { return cmp(v, other) <= 0 }
func (v Version) GreaterEq(other Version) bool { return cmp(v, other) >= 0 }

func (v Version) String() string { return string(v) }

// Satisfies reports whether v meets the constraint expressed as (op, required).
// op must be one of: "=", "==", "!=", "<", "<=", ">", ">=".
func (v Version) Satisfies(op string, required Version) (bool, error) {
	c := cmp(v, required)
	switch op {
	case "=", "==":
		return c == 0, nil
	case "!=":
		return c != 0, nil
	case "<":
		return c < 0, nil
	case "<=":
		return c <= 0, nil
	case ">":
		return c > 0, nil
	case ">=":
		return c >= 0, nil
	}
	return false, fmt.Errorf("unknown operator %q", op)
}
