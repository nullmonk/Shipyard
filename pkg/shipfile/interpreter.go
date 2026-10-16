package shipfile

import (
	"path/filepath"

	_ "github.com/go-python/gpython/stdlib"

	"github.com/go-python/gpython/py"

	// Blank imports register the built-in Python modules before any interpreter
	// is created; order does not matter.
	_ "shipyard/pkg/shipfile/modules/hashlib"
	_ "shipyard/pkg/shipfile/modules/json"
	_ "shipyard/pkg/shipfile/modules/ospath"
	_ "shipyard/pkg/shipfile/modules/re"
	shipmod "shipyard/pkg/shipfile/modules/shipyard"
)

// Interpreter is a gpython context with the shipyard Python package on sys.path.
type Interpreter struct {
	ctx     py.Context
	sandbox *Sandbox
}

// New creates an Interpreter. shipyardPkg must be the directory that contains
// the shipyard Python package (i.e. the parent of the shipyard/ folder), so
// that `import shipyard` resolves correctly.
func New(shipyardPkg string) (*Interpreter, error) {
	opts := py.DefaultContextOpts()
	// "" lets gpython resolve absolute paths: path.Join("", "/abs/p") = "/abs/p"
	opts.SysPaths = append([]string{""}, opts.SysPaths...)
	opts.SysPaths = append(opts.SysPaths, shipyardPkg)

	ctx := py.NewContext(opts)
	sb, err := newSandbox(ctx)
	if err != nil {
		ctx.Close()
		return nil, err
	}
	if err := shipmod.InitCodesearch(ctx); err != nil {
		ctx.Close()
		return nil, err
	}
	return &Interpreter{ctx: ctx, sandbox: sb}, nil
}

// RunShipfile executes the shipfile.py at path inside the interpreter context.
// The script's global namespace is returned so callers can inspect or call
// objects defined in the shipfile (e.g. the Shipfile class).
// The file is opened by absolute path; imports inside it use sys.path normally.
func (i *Interpreter) RunShipfile(path string) (*py.Module, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return py.RunFile(i.ctx, abs, py.CompileOpts{UseSysPaths: true}, nil)
}

// RunFile executes an arbitrary Python file inside the interpreter context.
func (i *Interpreter) RunFile(path string) (*py.Module, error) {
	return i.RunShipfile(path)
}

// Sandbox returns the Sandbox controlling execution, file-read, env, and env-pass-through
// permissions for this interpreter.
func (i *Interpreter) Sandbox() *Sandbox {
	return i.sandbox
}

// LoadShipfile compiles and executes content as a shipfile, then validates it.
// The full sandbox is active: no file reads or command execution during load.
// Returns the parsed Shipfile on success.
func (i *Interpreter) LoadShipfile(content string) (*Shipfile, error) {
	sf, err := LoadString(content, i)
	if err != nil {
		return nil, err
	}
	if err := sf.Validate(); err != nil {
		return nil, err
	}
	return sf, nil
}

// GetModule returns a module that has already been imported into the interpreter.
func (i *Interpreter) GetModule(name string) (*py.Module, error) {
	return i.ctx.GetModule(name)
}

// GetShipyardAttr returns an attribute from the imported shipyard module.
func (i *Interpreter) GetShipyardAttr(name string) (py.Object, error) {
	mod, err := i.ctx.GetModule("shipyard")
	if err != nil {
		return nil, err
	}
	return py.GetAttrString(mod, name)
}

// Context returns the underlying py.Context for low-level use.
func (i *Interpreter) Context() py.Context {
	return i.ctx
}

// Close releases resources held by the interpreter.
func (i *Interpreter) Close() {
	unregisterSandbox(i.ctx)
	i.ctx.Close()
}
