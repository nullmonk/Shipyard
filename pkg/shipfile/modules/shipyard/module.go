// Package shipmod registers the built-in "shipyard" Python module.
// Import it with _ to trigger registration:
//
//	import _ "shipyard/pkg/shipfile/modules/shipyard"
//
// Run() is locked by default. Call EnableRun(ctx) / DisableRun(ctx) from Go
// to unlock it only during the build phase (setup / build commands).
package shipmod

import (
	_ "embed"

	"github.com/go-python/gpython/py"
)

//go:embed builtins.py
var codeSrc string

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "shipyard",
			Doc:  "Shipyard built-ins: CodePatch, CodeFile, Version, Run, Script, Shipfile, Patcher, Builder, Context, Distro.",
		},
		Globals: py.StringDict{},
		Methods: []*py.Method{
			py.MustNewMethod("CodePatch", codePatchFactory, 0, "CodePatch(*files, versions=[], required=True) -> decorator"),
			py.MustNewMethod("Patch", codePatchFactory, 0, "Alias for CodePatch"),
			py.MustNewMethod("Run", runFn, 0, "Run(cmd, stdin='') -> RunResult  [build phase only]"),
			py.MustNewMethod("Variable", variableFactory, 0, "Variable(name, description='', value='', validate=None) -> Variable"),
			py.MustNewMethod("get_variables", getVariablesFn, 0, "get_variables(shipfile) -> list[dict]"),
			py.MustNewMethod("test", testFactory, 0, "test(name='', description='') -> decorator"),
			py.MustNewMethod("_is_subclass", isSubclassFn, 0, "_is_subclass(obj_or_cls, target_cls) -> bool"),
			py.MustNewMethod("_get_class", getClassFn, 0, "_get_class(obj_or_cls) -> class"),
			py.MustNewMethod("_has_own_attr", hasOwnAttrFn, 0, "_has_own_attr(obj_or_cls, attr) -> bool"),
			// validate is defined in builtins.py as a module-level function
		},
		CodeSrc: codeSrc,
	})
}

func resolvePyClass(obj py.Object) *py.Type {
	if t, ok := obj.(*py.Type); ok {
		if t.Name != "" {
			return t
		}
		return t.ObjectType
	}
	if obj != nil {
		return obj.Type()
	}
	return nil
}

func getClassFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return py.None, nil
	}
	if cls := resolvePyClass(args[0]); cls != nil {
		return cls, nil
	}
	return args[0], nil
}

func isSubclassFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) != 2 {
		return py.False, nil
	}
	typ := resolvePyClass(args[0])
	target := resolvePyClass(args[1])
	if typ == nil || target == nil {
		return py.False, nil
	}
	for _, b := range typ.Mro {
		if b == target {
			return py.True, nil
		}
	}
	for _, b := range typ.Bases {
		if b == target {
			return py.True, nil
		}
	}
	return py.False, nil
}

func hasOwnAttrFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) != 2 {
		return py.False, nil
	}
	typ := resolvePyClass(args[0])
	attrStr, ok := args[1].(py.String)
	if !ok || typ == nil {
		return py.False, nil
	}
	attr := string(attrStr)
	if _, exists := typ.Dict[attr]; exists {
		return py.True, nil
	}
	for _, base := range typ.Mro {
		b, ok := base.(*py.Type)
		if !ok || b == typ {
			continue
		}
		switch b.Name {
		case "Shipfile", "Patcher", "Builder", "Tester", "object":
			continue
		}
		if _, exists := b.Dict[attr]; exists {
			return py.True, nil
		}
	}
	return py.False, nil
}


