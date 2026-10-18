package shipmod

import (
	"fmt"

	"github.com/go-python/gpython/py"
)

var variableType = py.NewType("Variable", "A Shipfile configuration variable")

// Variable represents a single Shipfile variable with a name, description, string value, and optional validator.
type Variable struct {
	Name        string
	Description string
	Value       string
	ValidateFn  py.Object
}

func (v *Variable) Type() *py.Type { return variableType }

func isCallable(obj py.Object) bool {
	if obj == nil || obj == py.None {
		return false
	}
	_, ok := obj.(py.I__call__)
	return ok
}

// Validate calls the validator function if present and returns any exception it raises.
func (v *Variable) Validate(val string) error {
	_, err := v.ValidateAndResolve(val)
	return err
}

// ValidateAndResolve validates val against the validator function if present and returns the resolved Python object.
// If the validator returns None or True, py.String(val) is returned.
// Otherwise, the validator's return value is returned.
func (v *Variable) ValidateAndResolve(val string) (py.Object, error) {
	if v.ValidateFn == nil || v.ValidateFn == py.None {
		return py.String(val), nil
	}
	res, err := py.Call(v.ValidateFn, py.Tuple{py.String(val)}, nil)
	if err != nil {
		return nil, err
	}
	if res == nil || res == py.None || res == py.True {
		return py.String(val), nil
	}
	return res, nil
}

// SetAndValidate validates val and, if it passes, stores it on the variable.
func (v *Variable) SetAndValidate(val string) error {
	if err := v.Validate(val); err != nil {
		return err
	}
	v.Value = val
	return nil
}

func (v *Variable) M__getattribute__(name string) (py.Object, error) {
	switch name {
	case "name":
		return py.String(v.Name), nil
	case "description":
		return py.String(v.Description), nil
	case "value":
		return py.String(v.Value), nil
	case "__is_variable__":
		return py.True, nil
	case "validate":
		return py.NewMethod("validate", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
			targetVal := v.Value
			if len(args) > 0 {
				s, ok := args[0].(py.String)
				if !ok {
					return nil, py.ExceptionNewf(py.TypeError, "Variable %q value must be a string, got %s", v.Name, args[0].Type().Name)
				}
				targetVal = string(s)
			}
			return v.ValidateAndResolve(targetVal)
		}, 0, "validate([value]) -> Any")
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'Variable' object has no attribute %q", name)
}

func (v *Variable) M__setattr__(name string, val py.Object) (py.Object, error) {
	switch name {
	case "value":
		s, ok := val.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Variable %q value must be a string, got %s", v.Name, val.Type().Name)
		}
		if err := v.SetAndValidate(string(s)); err != nil {
			return nil, err
		}
		return py.None, nil
	case "description":
		s, ok := val.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Variable description must be a string, got %s", val.Type().Name)
		}
		v.Description = string(s)
		return py.None, nil
	case "name":
		s, ok := val.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Variable name must be a string, got %s", val.Type().Name)
		}
		v.Name = string(s)
		return py.None, nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'Variable' object has no attribute %q", name)
}

// variableFactory implements shipyard.Variable(name, description="", value="", validate=None)
func variableFactory(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	var nameObj, descObj, valObj, validateObj py.Object

	if len(args) > 0 {
		nameObj = args[0]
	} else if n, ok := kwargs["name"]; ok {
		nameObj = n
	}
	if nameObj == nil {
		return nil, py.ExceptionNewf(py.TypeError, "Variable() missing required argument: 'name'")
	}
	nameStr, ok := nameObj.(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Variable name must be a string, got %s", nameObj.Type().Name)
	}
	if string(nameStr) == "" {
		return nil, py.ExceptionNewf(py.ValueError, "Variable name must not be empty")
	}

	if len(args) > 1 {
		descObj = args[1]
	} else if d, ok := kwargs["description"]; ok {
		descObj = d
	}
	descStr := ""
	if descObj != nil && descObj != py.None {
		s, ok := descObj.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Variable description must be a string, got %s", descObj.Type().Name)
		}
		descStr = string(s)
	}

	if len(args) > 2 {
		valObj = args[2]
	} else if v, ok := kwargs["value"]; ok {
		valObj = v
	} else if d, ok := kwargs["default"]; ok {
		valObj = d
	}
	valStr := ""
	if valObj != nil && valObj != py.None {
		s, ok := valObj.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Variable %q value must be a string, got %s", string(nameStr), valObj.Type().Name)
		}
		valStr = string(s)
	}

	if len(args) > 3 {
		validateObj = args[3]
	} else if v, ok := kwargs["validate"]; ok {
		validateObj = v
	} else if v, ok := kwargs["validaet"]; ok {
		validateObj = v
	}
	if validateObj != nil && validateObj != py.None {
		if !isCallable(validateObj) {
			return nil, py.ExceptionNewf(py.TypeError, "Variable %q validate must be callable, got %s", string(nameStr), validateObj.Type().Name)
		}
	}

	v := &Variable{
		Name:        string(nameStr),
		Description: descStr,
		Value:       valStr,
		ValidateFn:  validateObj,
	}

	if err := v.Validate(valStr); err != nil {
		return nil, err
	}

	return v, nil
}

// ExtractVariables inspects a Shipfile class or instance for its Variables attribute
// (an array/list of Variable objects) and validates each entry.
func ExtractVariables(target py.Object) ([]*Variable, error) {
	if target == nil || target == py.None {
		return nil, nil
	}

	var typ *py.Type
	if t, ok := target.(*py.Type); ok {
		if t.Name != "" {
			typ = t
		} else if t.ObjectType != nil {
			typ = t.ObjectType
		}
	} else if clsObj, err := py.GetAttrString(target, "__class__"); err == nil {
		if t, ok := clsObj.(*py.Type); ok {
			typ = t
		}
	}

	var rawVars py.Object
	if v, err := py.GetAttrString(target, "Variables"); err == nil && v != nil && v != py.None {
		rawVars = v
	} else if typ != nil {
		if v, ok := typ.Dict["Variables"]; ok && v != nil && v != py.None {
			rawVars = v
		}
	}

	if rawVars == nil {
		return nil, nil
	}

	var seqItems []py.Object
	switch seq := rawVars.(type) {
	case *py.List:
		seqItems = seq.Items
	case py.Tuple:
		seqItems = seq
	default:
		return nil, py.ExceptionNewf(py.TypeError, "Shipfile.Variables must be a list of Variable objects, got %s", rawVars.Type().Name)
	}

	out := make([]*Variable, 0, len(seqItems))
	for _, item := range seqItems {
		v, ok := item.(*Variable)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Shipfile.Variables elements must be Variable objects, got %s", item.Type().Name)
		}
		if err := v.Validate(v.Value); err != nil {
			return nil, err
		}
		out = append(out, v)
	}

	return out, nil
}

// BuildVariablesDict extracts the Shipfile's Variable list, applies any overrides from values,
// validates and resolves all values, and returns a Python dict (py.StringDict) to attach to ctx.variables.
func BuildVariablesDict(target py.Object, values map[string]string) (py.StringDict, error) {
	vars, err := ExtractVariables(target)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]*Variable, len(vars))
	for _, v := range vars {
		byName[v.Name] = v
	}

	for k := range values {
		if _, exists := byName[k]; !exists {
			return nil, fmt.Errorf("unknown variable %q", k)
		}
	}

	dict := py.NewStringDict()
	for _, v := range vars {
		val := v.Value
		if ovVal, ok := values[v.Name]; ok {
			val = ovVal
		}
		resolved, err := v.ValidateAndResolve(val)
		if err != nil {
			return nil, err
		}
		dict[v.Name] = resolved
	}

	return dict, nil
}

func getVariablesFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) == 0 {
		return nil, py.ExceptionNewf(py.TypeError, "get_variables() takes at least 1 argument")
	}
	vars, err := ExtractVariables(args[0])
	if err != nil {
		return nil, err
	}

	results := py.NewList()
	for _, v := range vars {
		desc := v.Description
		if desc == "" {
			desc = v.Name
		}
		d := py.StringDict{
			"name":        py.String(v.Name),
			"description": py.String(desc),
			"value":       py.String(v.Value),
		}
		results.Append(d)
	}
	return results, nil
}
