package shipmod

import (
	_ "embed"
	"fmt"

	"github.com/go-python/gpython/py"
)

//go:embed codesearch/__init__.py
var codesearchInitSrc string

//go:embed codesearch/c.py
var codesearchCSrc string

//go:embed codesearch/go.py
var codesearchGoSrc string

//go:embed codesearch/python.py
var codesearchPySrc string

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "shipyard.codesearch",
			Doc:  "codesearch - structural regex combinators for common languages",
		},
		CodeSrc: codesearchInitSrc,
	})

	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "shipyard.codesearch.c",
			Doc:  "Structural regex combinators for C/C++ source",
		},
		CodeSrc: codesearchCSrc,
	})

	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "shipyard.codesearch.go",
			Doc:  "Structural regex combinators for Go source",
		},
		CodeSrc: codesearchGoSrc,
	})

	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "shipyard.codesearch.python",
			Doc:  "Structural regex combinators for Python source",
		},
		CodeSrc: codesearchPySrc,
	})
}

// InitCodesearch initializes and links shipyard.codesearch modules into the context.
func InitCodesearch(ctx py.Context) error {
	if err := py.Import(ctx, "shipyard"); err != nil {
		return fmt.Errorf("import shipyard: %w", err)
	}
	if err := py.Import(ctx, "shipyard.codesearch.c", "shipyard.codesearch.go", "shipyard.codesearch.python", "shipyard.codesearch"); err != nil {
		return fmt.Errorf("import codesearch submodules: %w", err)
	}

	shipyardMod, err := ctx.GetModule("shipyard")
	if err != nil {
		return fmt.Errorf("get shipyard module: %w", err)
	}
	csMod, err := ctx.GetModule("shipyard.codesearch")
	if err != nil {
		return fmt.Errorf("get shipyard.codesearch module: %w", err)
	}
	cMod, err := ctx.GetModule("shipyard.codesearch.c")
	if err != nil {
		return fmt.Errorf("get shipyard.codesearch.c module: %w", err)
	}
	goMod, err := ctx.GetModule("shipyard.codesearch.go")
	if err != nil {
		return fmt.Errorf("get shipyard.codesearch.go module: %w", err)
	}
	pyMod, err := ctx.GetModule("shipyard.codesearch.python")
	if err != nil {
		return fmt.Errorf("get shipyard.codesearch.python module: %w", err)
	}

	csMod.Globals["c"] = cMod
	csMod.Globals["go"] = goMod
	csMod.Globals["python"] = pyMod
	shipyardMod.Globals["codesearch"] = csMod

	return nil
}

