package shipyard

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/go-python/gpython/py"
	"shipyard/pkg/shipyard/modules/shipyard"
)

var (
	sandboxRegistryMu sync.RWMutex
	sandboxRegistry   = make(map[py.Context]*Sandbox)
)

func init() {
	shipmod.SetExecutionChecker(func(ctx py.Context) bool {
		sb := GetSandbox(ctx)
		if sb == nil {
			return false
		}
		return sb.ExecutionEnabled()
	})
	shipmod.SetRunGateHooks(
		func(ctx py.Context) error {
			sb := GetSandbox(ctx)
			if sb == nil {
				return fmt.Errorf("no sandbox found for context")
			}
			sb.SetExecution(true)
			return nil
		},
		func(ctx py.Context) error {
			sb := GetSandbox(ctx)
			if sb == nil {
				return fmt.Errorf("no sandbox found for context")
			}
			sb.SetExecution(false)
			return nil
		},
	)
}

func registerSandbox(ctx py.Context, sb *Sandbox) {
	sandboxRegistryMu.Lock()
	defer sandboxRegistryMu.Unlock()
	sandboxRegistry[ctx] = sb
}

func unregisterSandbox(ctx py.Context) {
	sandboxRegistryMu.Lock()
	defer sandboxRegistryMu.Unlock()
	delete(sandboxRegistry, ctx)
}

// GetSandbox returns the Sandbox associated with the given py.Context, or nil.
func GetSandbox(ctx py.Context) *Sandbox {
	sandboxRegistryMu.RLock()
	defer sandboxRegistryMu.RUnlock()
	return sandboxRegistry[ctx]
}

// Sandbox enforces restrictions on gpython execution, filesystem access,
// and environment variable pass-through. State is stored purely in Go memory.
type Sandbox struct {
	mu             sync.RWMutex
	ctx            py.Context
	execution      bool
	fileRead       bool
	envPassThrough bool
}

// ExecutionEnabled reports whether command execution (os.system, shipyard.Run) is permitted.
func (s *Sandbox) ExecutionEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.execution
}

// SetExecution toggles command execution permission.
func (s *Sandbox) SetExecution(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execution = enabled
}

// FileReadEnabled reports whether filesystem read and directory access is permitted.
func (s *Sandbox) FileReadEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fileRead
}

// SetFileRead toggles filesystem read and directory access permission.
func (s *Sandbox) SetFileRead(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileRead = enabled
}

// EnvPassThroughEnabled reports whether host environment variable pass-through is permitted.
func (s *Sandbox) EnvPassThroughEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.envPassThrough
}

// SetEnvPassThrough toggles host environment variable pass-through.
// When disabled, os.environ is cleared and os.getenv returns default/None.
func (s *Sandbox) SetEnvPassThrough(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.envPassThrough = enabled
	s.syncEnvironLocked()
}

func (s *Sandbox) syncEnvironLocked() {
	osMod, err := s.ctx.GetModule("os")
	if err != nil {
		return
	}
	envObj, ok := osMod.Globals["environ"]
	if !ok {
		return
	}
	envDict, ok := envObj.(py.StringDict)
	if !ok {
		return
	}

	for k := range envDict {
		delete(envDict, k)
	}

	if s.envPassThrough {
		for _, evar := range os.Environ() {
			kv := strings.SplitN(evar, "=", 2)
			if len(kv) == 2 {
				envDict[kv[0]] = py.String(kv[1])
			}
		}
	}
}

// newSandbox wraps dangerous primitives in the given py.Context and registers the sandbox.
func newSandbox(ctx py.Context) (*Sandbox, error) {
	sb := &Sandbox{
		ctx:            ctx,
		execution:      false,
		fileRead:       false,
		envPassThrough: false,
	}

	// 1. Wrap builtins.open
	builtinsMod := ctx.Store().Builtins
	if origOpen, ok := builtinsMod.Globals["open"].(*py.Method); ok {
		wrappedOpen := py.MustNewMethod("open", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
			if !sb.FileReadEnabled() {
				return nil, py.ExceptionNewf(py.PermissionError, "open() is not allowed: file read is disabled")
			}
			return origOpen.CallWithKeywords(origOpen.Module, args, kwargs)
		}, origOpen.Flags, origOpen.Doc)
		wrappedOpen.Module = builtinsMod
		builtinsMod.Globals["open"] = wrappedOpen
	}

	// 2. Pre-import and wrap "os"
	if err := py.Import(ctx, "os"); err != nil {
		return nil, fmt.Errorf("sandbox pre-import os: %w", err)
	}
	osMod, err := ctx.GetModule("os")
	if err != nil {
		return nil, fmt.Errorf("sandbox get os module: %w", err)
	}

	// Wrap execution methods in os
	if origSystem, ok := osMod.Globals["system"].(*py.Method); ok {
		wrappedSystem := py.MustNewMethod("system", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
			if !sb.ExecutionEnabled() {
				return nil, py.ExceptionNewf(py.PermissionError, "os.system() is not allowed: execution is disabled")
			}
			return origSystem.CallWithKeywords(origSystem.Module, args, kwargs)
		}, origSystem.Flags, origSystem.Doc)
		wrappedSystem.Module = osMod
		osMod.Globals["system"] = wrappedSystem
	}

	if origExit, ok := osMod.Globals["_exit"].(*py.Method); ok {
		wrappedExit := py.MustNewMethod("_exit", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
			return nil, py.ExceptionNewf(py.PermissionError, "os._exit() is not allowed in sandbox")
		}, origExit.Flags, origExit.Doc)
		wrappedExit.Module = osMod
		osMod.Globals["_exit"] = wrappedExit
	}

	// Wrap file read & filesystem modification methods in os
	osFileMethods := []string{
		"listdir", "getcwd", "getcwdb", "chdir",
		"mkdir", "makedirs", "remove", "removedirs", "rmdir",
		"close", "fdopen",
	}
	for _, name := range osFileMethods {
		if orig, ok := osMod.Globals[name].(*py.Method); ok {
			methodName := name
			origMethod := orig
			wrapped := py.MustNewMethod(methodName, func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
				if !sb.FileReadEnabled() {
					return nil, py.ExceptionNewf(py.PermissionError, "os.%s() is not allowed: file read is disabled", methodName)
				}
				return origMethod.CallWithKeywords(origMethod.Module, args, kwargs)
			}, orig.Flags, orig.Doc)
			wrapped.Module = osMod
			osMod.Globals[methodName] = wrapped
		}
	}

	// Wrap environment methods in os
	if origGetenv, ok := osMod.Globals["getenv"].(*py.Method); ok {
		wrappedGetenv := py.MustNewMethod("getenv", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
			if !sb.EnvPassThroughEnabled() {
				if len(args) < 1 {
					return nil, py.ExceptionNewf(py.TypeError, "getenv() requires at least 1 argument")
				}
				key, ok := args[0].(py.String)
				if !ok {
					return nil, py.ExceptionNewf(py.TypeError, "str expected, not %s", args[0].Type().Name)
				}
				if envDict, ok := osMod.Globals["environ"].(py.StringDict); ok {
					if val, exists := envDict[string(key)]; exists {
						return val, nil
					}
				}
				if len(args) >= 2 {
					return args[1], nil
				}
				return py.None, nil
			}
			return origGetenv.CallWithKeywords(origGetenv.Module, args, kwargs)
		}, origGetenv.Flags, origGetenv.Doc)
		wrappedGetenv.Module = osMod
		osMod.Globals["getenv"] = wrappedGetenv
	}

	if origPutenv, ok := osMod.Globals["putenv"].(*py.Method); ok {
		wrappedPutenv := py.MustNewMethod("putenv", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
			if !sb.EnvPassThroughEnabled() {
				if len(args) != 2 {
					return nil, py.ExceptionNewf(py.TypeError, "putenv() requires key and value")
				}
				k, ok1 := args[0].(py.String)
				v, ok2 := args[1].(py.String)
				if !ok1 || !ok2 {
					return nil, py.ExceptionNewf(py.TypeError, "str expected")
				}
				if envDict, ok := osMod.Globals["environ"].(py.StringDict); ok {
					envDict[string(k)] = v
				}
				return py.None, nil
			}
			return origPutenv.CallWithKeywords(origPutenv.Module, args, kwargs)
		}, origPutenv.Flags, origPutenv.Doc)
		wrappedPutenv.Module = osMod
		osMod.Globals["putenv"] = wrappedPutenv
	}

	if origUnsetenv, ok := osMod.Globals["unsetenv"].(*py.Method); ok {
		wrappedUnsetenv := py.MustNewMethod("unsetenv", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
			if !sb.EnvPassThroughEnabled() {
				if len(args) != 1 {
					return nil, py.ExceptionNewf(py.TypeError, "unsetenv() requires key")
				}
				k, ok := args[0].(py.String)
				if !ok {
					return nil, py.ExceptionNewf(py.TypeError, "str expected")
				}
				if envDict, ok := osMod.Globals["environ"].(py.StringDict); ok {
					delete(envDict, string(k))
				}
				return py.None, nil
			}
			return origUnsetenv.CallWithKeywords(origUnsetenv.Module, args, kwargs)
		}, origUnsetenv.Flags, origUnsetenv.Doc)
		wrappedUnsetenv.Module = osMod
		osMod.Globals["unsetenv"] = wrappedUnsetenv
	}

	// 3. Pre-import and wrap "glob"
	if err := py.Import(ctx, "glob"); err == nil {
		if globMod, err := ctx.GetModule("glob"); err == nil {
			if origGlob, ok := globMod.Globals["glob"].(*py.Method); ok {
				wrappedGlob := py.MustNewMethod("glob", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
					if !sb.FileReadEnabled() {
						return nil, py.ExceptionNewf(py.PermissionError, "glob.glob() is not allowed: file read is disabled")
					}
					return origGlob.CallWithKeywords(origGlob.Module, args, kwargs)
				}, origGlob.Flags, origGlob.Doc)
				wrappedGlob.Module = globMod
				globMod.Globals["glob"] = wrappedGlob
			}
		}
	}

	// 4. Pre-import and wrap "tempfile"
	if err := py.Import(ctx, "tempfile"); err == nil {
		if tempMod, err := ctx.GetModule("tempfile"); err == nil {
			tempMethods := []string{"mkstemp", "mkdtemp", "gettempdir", "gettempdirb"}
			for _, name := range tempMethods {
				if orig, ok := tempMod.Globals[name].(*py.Method); ok {
					methodName := name
					origMethod := orig
					wrapped := py.MustNewMethod(methodName, func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
						if !sb.FileReadEnabled() {
							return nil, py.ExceptionNewf(py.PermissionError, "tempfile.%s() is not allowed: file read is disabled", methodName)
						}
						return origMethod.CallWithKeywords(origMethod.Module, args, kwargs)
					}, orig.Flags, orig.Doc)
					wrapped.Module = tempMod
					tempMod.Globals[methodName] = wrapped
				}
			}
		}
	}

	sb.syncEnvironLocked()
	registerSandbox(ctx, sb)
	return sb, nil
}

