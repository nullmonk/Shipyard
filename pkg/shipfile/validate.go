package shipfile

import (
	"fmt"

	"github.com/go-python/gpython/py"
)

// Validate calls shipyard.validate() on the loaded Shipfile instance.
// This handles all subclass checks (Patcher, Builder, Tester) via isinstance,
// and also works when Shipfile does not explicitly subclass shipyard.Shipfile.
func (s *Shipfile) Validate() error {
	if s.sfInst == nil {
		return fmt.Errorf("shipfile instance not initialized")
	}
	validateFn, err := s.interp.GetShipyardAttr("validate")
	if err != nil {
		return fmt.Errorf("get shipyard.validate: %w", err)
	}
	if _, err := py.Call(validateFn, py.Tuple{s.sfInst}, nil); err != nil {
		return err
	}
	return nil
}

// Validate loads and validates a shipfile using the provided interpreter.
func Validate(path string, interp *Interpreter) (*Shipfile, error) {
	sf, err := Load(path, interp)
	if err != nil {
		return nil, err
	}
	if err := sf.Validate(); err != nil {
		return sf, err
	}
	return sf, nil
}

// ValidateDefault loads and validates a shipfile with default package root.
func ValidateDefault(path string, pkgDir string) (*Shipfile, error) {
	sf, err := LoadDefault(path, pkgDir)
	if err != nil {
		return nil, err
	}
	if err := sf.Validate(); err != nil {
		return sf, err
	}
	return sf, nil
}
