// Package shipyard provides a gpython interpreter pre-configured to run shipfiles.
package shipyard

import (
	"path/filepath"

	_ "github.com/go-python/gpython/stdlib"

	"github.com/go-python/gpython/py"

	// Blank imports register the built-in Python modules before any interpreter
	// is created; order does not matter.
	_ "shipyard/pkg/shipyard/modules/re"
	_ "shipyard/pkg/shipyard/modules/shipyard"
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

// Sandbox returns the Sandbox instance protecting this interpreter.
func (i *Interpreter) Sandbox() *Sandbox {
	return i.sandbox
}

// SetExecution toggles command execution permission (os.system, shipyard.Run).
func (i *Interpreter) SetExecution(enabled bool) {
	i.sandbox.SetExecution(enabled)
}

// ExecutionEnabled reports whether command execution is enabled.
func (i *Interpreter) ExecutionEnabled() bool {
	return i.sandbox.ExecutionEnabled()
}

// SetFileRead toggles filesystem read and directory access permission.
func (i *Interpreter) SetFileRead(enabled bool) {
	i.sandbox.SetFileRead(enabled)
}

// FileReadEnabled reports whether filesystem read is enabled.
func (i *Interpreter) FileReadEnabled() bool {
	return i.sandbox.FileReadEnabled()
}

// SetEnvPassThrough toggles environment variable pass-through from the host.
func (i *Interpreter) SetEnvPassThrough(enabled bool) {
	i.sandbox.SetEnvPassThrough(enabled)
}

// EnvPassThroughEnabled reports whether environment variable pass-through is enabled.
func (i *Interpreter) EnvPassThroughEnabled() bool {
	return i.sandbox.EnvPassThroughEnabled()
}

// EnableRun unlocks command execution (os.system, shipyard.Run) for this interpreter.
// Call it before invoking setup or build phases, then DisableRun afterwards.
func (i *Interpreter) EnableRun() error {
	i.SetExecution(true)
	return nil
}

// DisableRun locks command execution again after a build phase completes.
func (i *Interpreter) DisableRun() error {
	i.SetExecution(false)
	return nil
}

// GetModule returns a module that has already been imported into the interpreter.
func (i *Interpreter) GetModule(name string) (*py.Module, error) {
	return i.ctx.GetModule(name)
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
