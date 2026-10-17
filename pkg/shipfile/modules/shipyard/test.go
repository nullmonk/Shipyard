package shipmod

import "github.com/go-python/gpython/py"

var testDecoratorType = py.NewType("TestDecorator", "")

type testDecorator struct {
	name        string
	description string
}

func (d *testDecorator) Type() *py.Type { return testDecoratorType }

func (d *testDecorator) M__call__(args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) != 1 {
		return nil, py.ExceptionNewf(py.TypeError, "test decorator takes exactly 1 argument")
	}
	fn := args[0]
	py.SetAttrString(fn, "__test_name", py.String(d.name))
	py.SetAttrString(fn, "__test_description", py.String(d.description))
	return fn, nil
}

func testFactory(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	name := ""
	description := ""
	if len(args) >= 1 {
		if s, ok := args[0].(py.String); ok {
			name = string(s)
		}
	}
	if len(args) >= 2 {
		if s, ok := args[1].(py.String); ok {
			description = string(s)
		}
	}
	if n, ok := kwargs["name"]; ok {
		if s, ok := n.(py.String); ok {
			name = string(s)
		}
	}
	if d, ok := kwargs["description"]; ok {
		if s, ok := d.(py.String); ok {
			description = string(s)
		}
	}
	return &testDecorator{name: name, description: description}, nil
}
