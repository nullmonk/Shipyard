package shipmod

import (
	"github.com/go-python/gpython/py"
)

var variableDecoratorType = py.NewType("VariableDecorator", "Decorator for Shipfile variables")

type variableDecorator struct {
	description string
	validate    py.Object
	defaultValue string
	hasDefault  bool
}

func (d *variableDecorator) Type() *py.Type { return variableDecoratorType }

func isCallable(obj py.Object) bool {
	if obj == nil {
		return false
	}
	_, ok := obj.(py.I__call__)
	return ok
}

func (d *variableDecorator) M__call__(args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) != 1 {
		return nil, py.ExceptionNewf(py.TypeError, "Variable decorator takes exactly 1 argument (the function or value to decorate)")
	}

	target := args[0]

	// 1. Target is a function / callable
	if isCallable(target) {
		name := ""
		if n, err := py.GetAttrString(target, "__name__"); err == nil {
			if s, ok := n.(py.String); ok {
				name = string(s)
			}
		}

		py.SetAttrString(target, "__is_variable__", py.True)
		py.SetAttrString(target, "__variable_description__", py.String(d.description))
		if d.validate != nil {
			py.SetAttrString(target, "__variable_validate__", d.validate)
		}

		// Try invoking to validate string return type & run validator
		var retVal py.Object
		var callErr error
		retVal, callErr = py.Call(target, py.Tuple{}, nil)
		if callErr != nil {
			// Might require 'self' or ctx
			retVal, callErr = py.Call(target, py.Tuple{py.None}, nil)
		}

		if callErr == nil && retVal != nil {
			s, ok := retVal.(py.String)
			if !ok {
				return nil, py.ExceptionNewf(py.TypeError, "Variable %q must return a string, got %s", name, retVal.Type().Name)
			}
			valStr := string(s)

			// Run custom validator if provided
			if err := runValidator(d.validate, name, valStr); err != nil {
				return nil, err
			}
			py.SetAttrString(target, "__variable_value__", s)
		} else if d.hasDefault {
			if err := runValidator(d.validate, name, d.defaultValue); err != nil {
				return nil, err
			}
			py.SetAttrString(target, "__variable_value__", py.String(d.defaultValue))
		}

		return target, nil
	}

	// 2. Target is a string value directly: Variable("desc")("value")
	if s, ok := target.(py.String); ok {
		valStr := string(s)
		if err := runValidator(d.validate, "", valStr); err != nil {
			return nil, err
		}
		// Return a string with variable metadata attached
		py.SetAttrString(s, "__is_variable__", py.True)
		py.SetAttrString(s, "__variable_description__", py.String(d.description))
		py.SetAttrString(s, "__variable_value__", s)
		if d.validate != nil {
			py.SetAttrString(s, "__variable_validate__", d.validate)
		}
		return s, nil
	}

	return nil, py.ExceptionNewf(py.TypeError, "Variable can only decorate functions or strings, got %s", target.Type().Name)
}

func runValidator(valFn py.Object, varName, value string) error {
	if valFn == nil || valFn == py.None {
		return nil
	}

	res, err := py.Call(valFn, py.Tuple{py.String(value)}, nil)
	if err != nil {
		return err
	}

	if res == nil || res == py.None || res == py.True {
		return nil
	}

	if res == py.False {
		name := varName
		if name == "" {
			name = "variable"
		}
		return py.ExceptionNewf(py.ValueError, "Variable %q validation failed for value %q", name, value)
	}

	if s, ok := res.(py.String); ok && string(s) != "" {
		name := varName
		if name == "" {
			name = "variable"
		}
		return py.ExceptionNewf(py.ValueError, "Variable %q validation failed: %s", name, string(s))
	}

	return nil
}

func variableFactory(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	desc := ""
	if len(args) > 0 {
		s, ok := args[0].(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Variable description must be a string, got %s", args[0].Type().Name)
		}
		desc = string(s)
	} else if d, ok := kwargs["description"]; ok {
		s, ok := d.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Variable description must be a string, got %s", d.Type().Name)
		}
		desc = string(s)
	}

	var validateFn py.Object
	if v, ok := kwargs["validate"]; ok && v != py.None {
		if !isCallable(v) {
			return nil, py.ExceptionNewf(py.TypeError, "validate must be a callable function, got %s", v.Type().Name)
		}
		validateFn = v
	} else if v, ok := kwargs["validaet"]; ok && v != py.None { // Support typo alias from prompt
		if !isCallable(v) {
			return nil, py.ExceptionNewf(py.TypeError, "validate must be a callable function, got %s", v.Type().Name)
		}
		validateFn = v
	}

	defVal := ""
	hasDef := false
	if len(args) > 1 {
		s, ok := args[1].(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Variable default value must be a string, got %s", args[1].Type().Name)
		}
		defVal = string(s)
		hasDef = true
	} else if d, ok := kwargs["default"]; ok && d != py.None {
		s, ok := d.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Variable default value must be a string, got %s", d.Type().Name)
		}
		defVal = string(s)
		hasDef = true
	}

	if hasDef {
		if err := runValidator(validateFn, "", defVal); err != nil {
			return nil, err
		}
	}

	return &variableDecorator{
		description:  desc,
		validate:     validateFn,
		defaultValue: defVal,
		hasDefault:   hasDef,
	}, nil
}
