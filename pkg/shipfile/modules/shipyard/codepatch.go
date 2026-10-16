package shipmod

import (
	"github.com/go-python/gpython/py"
)

var decoratorType = py.NewType("CodePatchDecorator", "")

type codePatchDecorator struct {
	files    py.Tuple
	versions py.Tuple
	required bool
}

func (d *codePatchDecorator) Type() *py.Type { return decoratorType }

func (d *codePatchDecorator) M__call__(args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) != 1 {
		return nil, py.ExceptionNewf(py.TypeError, "CodePatch decorator takes exactly 1 argument")
	}
	fn := args[0]
	py.SetAttrString(fn, "__patch_files", d.files)
	py.SetAttrString(fn, "__patch_versions", d.versions)
	if d.required {
		py.SetAttrString(fn, "__patch_required", py.True)
	} else {
		py.SetAttrString(fn, "__patch_required", py.False)
	}
	py.SetAttrString(fn, "__patch_has_run", py.False)
	return fn, nil
}

func codePatchFactory(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	files := make(py.Tuple, len(args))
	copy(files, args)

	var versions py.Tuple
	if v, ok := kwargs["versions"]; ok {
		switch t := v.(type) {
		case py.Tuple:
			versions = t
		case *py.List:
			versions = make(py.Tuple, len(t.Items))
			copy(versions, t.Items)
		}
	}

	required := true
	if r, ok := kwargs["required"]; ok {
		required = r != py.False
	}

	return &codePatchDecorator{files: files, versions: versions, required: required}, nil
}
