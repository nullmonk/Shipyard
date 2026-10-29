package shipfile

import (
	"fmt"

	"github.com/go-python/gpython/py"
)

// This file holds helpers for invoking the version-mapping hooks a Shipfile
// may define (tag_to_version, version_to_tag, is_version_ignored). The base
// Shipfile class provides identity/default implementations, so these helpers
// work even when a Shipfile does not override them.

// ensureShipfileInst returns the cached Shipfile instance, instantiating it
// on first use.
func (s *Shipfile) ensureShipfileInst() (py.Object, error) {
	if s.sfInst != nil {
		return s.sfInst, nil
	}
	if s.sfClass == nil {
		return nil, fmt.Errorf("shipfile class is not initialized")
	}
	inst, err := py.Call(s.sfClass, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("instantiate Shipfile: %w", err)
	}
	s.sfInst = inst
	return inst, nil
}

// callShipfileFunc invokes a Shipfile method with the given arguments,
// following the same self-convention as CodePatch functions: if the
// underlying function's first parameter is named "self", the instance is
// passed as the first argument.
func (s *Shipfile) callShipfileFunc(name string, args ...py.Object) (py.Object, error) {
	if s.interp == nil {
		return nil, fmt.Errorf("shipfile interpreter is not initialized")
	}
	inst, err := s.ensureShipfileInst()
	if err != nil {
		return nil, err
	}
	fn, found := findMethodInType(s.sfClass, inst, name)
	if !found {
		return nil, fmt.Errorf("no method %s found on Shipfile", name)
	}
	if pyFn, ok := fn.(*py.Function); ok && pyFn.Code != nil {
		hasSelf := len(pyFn.Code.Varnames) > 0 && pyFn.Code.Varnames[0] == "self"
		if hasSelf {
			if inst == nil {
				return nil, fmt.Errorf("method %s requires an instance", name)
			}
			return py.Call(fn, append(py.Tuple{inst}, args...), nil)
		}
		return py.Call(fn, py.Tuple(args), nil)
	}
	if inst != nil {
		return py.Call(fn, append(py.Tuple{inst}, args...), nil)
	}
	return py.Call(fn, py.Tuple(args), nil)
}

// TagToVersion converts an upstream tag (e.g. "V_9_9_P2") to a version string
// (e.g. "9.9p1") using the Shipfile's tag_to_version hook.
func (s *Shipfile) TagToVersion(tag string) (string, error) {
	res, err := s.callShipfileFunc("tag_to_version", py.String(tag))
	if err != nil {
		return "", err
	}
	str, ok := res.(py.String)
	if !ok {
		return "", fmt.Errorf("tag_to_version(%q) returned %s, want str", tag, res.Type().Name)
	}
	return string(str), nil
}

// VersionToTag converts a version string (e.g. "9.9p1") to an upstream tag
// (e.g. "V_9_9_P2") using the Shipfile's version_to_tag hook.
func (s *Shipfile) VersionToTag(version string) (string, error) {
	res, err := s.callShipfileFunc("version_to_tag", py.String(version))
	if err != nil {
		return "", err
	}
	str, ok := res.(py.String)
	if !ok {
		return "", fmt.Errorf("version_to_tag(%q) returned %s, want str", version, res.Type().Name)
	}
	return string(str), nil
}

// IsVersionIgnored reports whether the Shipfile wants a version skipped,
// using its is_version_ignored hook.
func (s *Shipfile) IsVersionIgnored(version string) (bool, error) {
	res, err := s.callShipfileFunc("is_version_ignored", py.String(version))
	if err != nil {
		return false, err
	}
	return res == py.True, nil
}
