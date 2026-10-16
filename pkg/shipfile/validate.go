package shipfile

import (
	"fmt"

	"github.com/go-python/gpython/py"
)

// Validate calls the Python validate() method on the loaded Shipfile instance.
func (s *Shipfile) Validate() error {
	if s.sfInst == nil {
		return fmt.Errorf("shipfile instance not initialized")
	}
	meth, err := py.GetAttrString(s.sfInst, "validate")
	if err != nil {
		return fmt.Errorf("shipfile has no validate method: %w", err)
	}
	if _, err := py.Call(meth, py.Tuple{}, nil); err != nil {
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
