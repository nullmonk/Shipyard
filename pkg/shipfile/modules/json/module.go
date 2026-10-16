// Package jsonmod implements the Python "json" module for gpython.
package jsonmod

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/go-python/gpython/py"
)

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "json",
			Doc:  "JSON (JavaScript Object Notation) encoder and decoder.",
		},
		Methods: []*py.Method{
			py.MustNewMethod("loads", jsonLoads, 0, "loads(s) -> object\nDeserialize s (a str instance containing a JSON document) to a Python object."),
			py.MustNewMethod("dumps", jsonDumps, 0, "dumps(obj, indent=None, sort_keys=False) -> str\nSerialize obj to a JSON formatted str."),
			py.MustNewMethod("load", jsonLoad, 0, "load(fp) -> object\nDeserialize fp (a .read()-supporting file-like object containing a JSON document) to a Python object."),
			py.MustNewMethod("dump", jsonDump, 0, "dump(obj, fp, indent=None, sort_keys=False)\nSerialize obj as a JSON formatted stream to fp."),
		},
	})
}

func goValueToPy(val interface{}) (py.Object, error) {
	if val == nil {
		return py.None, nil
	}
	switch v := val.(type) {
	case bool:
		if v {
			return py.True, nil
		}
		return py.False, nil
	case float64:
		if math.Trunc(v) == v && !math.IsNaN(v) && !math.IsInf(v, 0) {
			return py.Int(int64(v)), nil
		}
		return py.Float(v), nil
	case string:
		return py.String(v), nil
	case []interface{}:
		items := make([]py.Object, len(v))
		for i, item := range v {
			pyItem, err := goValueToPy(item)
			if err != nil {
				return nil, err
			}
			items[i] = pyItem
		}
		return &py.List{Items: items}, nil
	case map[string]interface{}:
		dict := py.NewStringDictSized(len(v))
		for k, item := range v {
			pyItem, err := goValueToPy(item)
			if err != nil {
				return nil, err
			}
			dict[k] = pyItem
		}
		return dict, nil
	default:
		return nil, fmt.Errorf("unsupported JSON type: %T", val)
	}
}

func pyValueToGo(obj py.Object) (interface{}, error) {
	if obj == py.None {
		return nil, nil
	}
	switch v := obj.(type) {
	case py.Bool:
		return bool(v), nil
	case py.Int:
		return int64(v), nil
	case py.Float:
		return float64(v), nil
	case py.String:
		return string(v), nil
	case py.Bytes:
		return string(v), nil
	case *py.List:
		res := make([]interface{}, len(v.Items))
		for i, item := range v.Items {
			goItem, err := pyValueToGo(item)
			if err != nil {
				return nil, err
			}
			res[i] = goItem
		}
		return res, nil
	case py.Tuple:
		res := make([]interface{}, len(v))
		for i, item := range v {
			goItem, err := pyValueToGo(item)
			if err != nil {
				return nil, err
			}
			res[i] = goItem
		}
		return res, nil
	case py.StringDict:
		res := make(map[string]interface{}, len(v))
		for k, val := range v {
			goVal, err := pyValueToGo(val)
			if err != nil {
				return nil, err
			}
			res[k] = goVal
		}
		return res, nil
	default:
		return nil, fmt.Errorf("type %s is not JSON serializable", obj.Type().Name)
	}
}

func jsonLoads(_ py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "loads() requires at least 1 argument")
	}
	var srcStr string
	switch s := args[0].(type) {
	case py.String:
		srcStr = string(s)
	case py.Bytes:
		srcStr = string(s)
	default:
		return nil, py.ExceptionNewf(py.TypeError, "the JSON object must be str or bytes, not %s", args[0].Type().Name)
	}

	var parsed interface{}
	if err := json.Unmarshal([]byte(srcStr), &parsed); err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "JSON decode error: %v", err)
	}

	return goValueToPy(parsed)
}

func jsonDumps(_ py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "dumps() requires at least 1 argument")
	}
	goVal, err := pyValueToGo(args[0])
	if err != nil {
		return nil, py.ExceptionNewf(py.TypeError, "%v", err)
	}

	indentStr := ""
	var indentObj py.Object
	if len(args) >= 2 {
		indentObj = args[1]
	} else if kwIndent, ok := kwargs["indent"]; ok {
		indentObj = kwIndent
	}

	if indentObj != nil && indentObj != py.None {
		switch iv := indentObj.(type) {
		case py.Int:
			n := int(iv)
			for i := 0; i < n; i++ {
				indentStr += " "
			}
		case py.String:
			indentStr = string(iv)
		}
	}

	var out []byte
	if indentStr != "" {
		out, err = json.MarshalIndent(goVal, "", indentStr)
	} else {
		out, err = json.Marshal(goVal)
	}
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "JSON encode error: %v", err)
	}

	return py.String(string(out)), nil
}

func jsonLoad(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "load() requires at least 1 argument")
	}
	fp := args[0]
	readAttr, err := py.GetAttrString(fp, "read")
	if err != nil {
		return nil, py.ExceptionNewf(py.TypeError, "object has no attribute 'read'")
	}
	data, err := py.Call(readAttr, py.Tuple{}, nil)
	if err != nil {
		return nil, err
	}
	return jsonLoads(self, py.Tuple{data}, kwargs)
}

func jsonDump(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 2 {
		return nil, py.ExceptionNewf(py.TypeError, "dump() requires at least 2 arguments (obj, fp)")
	}
	obj := args[0]
	fp := args[1]
	dumpsArgs := py.Tuple{obj}
	if len(args) > 2 {
		dumpsArgs = append(dumpsArgs, args[2:]...)
	}
	jsonStr, err := jsonDumps(self, dumpsArgs, kwargs)
	if err != nil {
		return nil, err
	}

	writeAttr, err := py.GetAttrString(fp, "write")
	if err != nil {
		return nil, py.ExceptionNewf(py.TypeError, "object has no attribute 'write'")
	}
	_, err = py.Call(writeAttr, py.Tuple{jsonStr}, nil)
	if err != nil {
		return nil, err
	}
	return py.None, nil
}
