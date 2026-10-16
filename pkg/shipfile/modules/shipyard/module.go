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
		},
		CodeSrc: codeSrc,
	})
}
