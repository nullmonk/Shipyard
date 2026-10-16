package shipyard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/go-python/gpython/py"
	ospath "shipyard/pkg/shipyard/modules/ospath"
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
	ospath.SetFileReadChecker(func(ctx py.Context) bool {
		if ctx == nil {
			return false
		}
		sb := GetSandbox(ctx)
		if sb == nil {
			return false
		}
		return sb.FileReadEnabled()
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

	// Pre-import and attach os.path
	if err := py.Import(ctx, "os.path"); err != nil {
		return nil, fmt.Errorf("sandbox pre-import os.path: %w", err)
	}
	osPathMod, err := ctx.GetModule("os.path")
	if err != nil {
		return nil, fmt.Errorf("sandbox get os.path module: %w", err)
	}
	osMod.Globals["path"] = osPathMod
	osPathMod.Globals["path"] = osPathMod

	// Add os.stat, os.lstat, os.walk, and os.stat_result
	statMethod := makeStatMethod(sb, osMod)
	osMod.Globals["stat"] = statMethod
	osMod.Globals["lstat"] = statMethod
	osMod.Globals["walk"] = makeWalkMethod(sb, osMod)
	osMod.Globals["stat_result"] = statResultType

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

var statResultType = py.NewType("os.stat_result", "stat_result: Result from stat, fstat, or lstat.")

type statResult struct {
	mode  int64
	ino   int64
	dev   int64
	nlink int64
	uid   int64
	gid   int64
	size  int64
	atime float64
	mtime float64
	ctime float64
}

func (s *statResult) Type() *py.Type { return statResultType }

func (s *statResult) asTuple() py.Tuple {
	return py.Tuple{
		py.Int(s.mode),
		py.Int(s.ino),
		py.Int(s.dev),
		py.Int(s.nlink),
		py.Int(s.uid),
		py.Int(s.gid),
		py.Int(s.size),
		py.Float(s.atime),
		py.Float(s.mtime),
		py.Float(s.ctime),
	}
}

func (s *statResult) M__len__() (py.Object, error) {
	return py.Int(10), nil
}

func (s *statResult) M__iter__() (py.Object, error) {
	return py.NewIterator(s.asTuple()), nil
}

func (s *statResult) M__getitem__(key py.Object) (py.Object, error) {
	idxObj, ok := key.(py.Int)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "tuple indices must be integers, not %s", key.Type().Name)
	}
	t := s.asTuple()
	idx := int(idxObj)
	if idx < 0 {
		idx += len(t)
	}
	if idx < 0 || idx >= len(t) {
		return nil, py.ExceptionNewf(py.IndexError, "tuple index out of range")
	}
	return t[idx], nil
}

func (s *statResult) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "st_mode":
		return py.Int(s.mode), nil
	case "st_ino":
		return py.Int(s.ino), nil
	case "st_dev":
		return py.Int(s.dev), nil
	case "st_nlink":
		return py.Int(s.nlink), nil
	case "st_uid":
		return py.Int(s.uid), nil
	case "st_gid":
		return py.Int(s.gid), nil
	case "st_size":
		return py.Int(s.size), nil
	case "st_atime":
		return py.Float(s.atime), nil
	case "st_mtime":
		return py.Float(s.mtime), nil
	case "st_ctime":
		return py.Float(s.ctime), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'os.stat_result' object has no attribute '%s'", name)
}

func (s *statResult) M__repr__() (py.Object, error) {
	return py.String(fmt.Sprintf(
		"os.stat_result(st_mode=%d, st_ino=%d, st_dev=%d, st_nlink=%d, st_uid=%d, st_gid=%d, st_size=%d, st_atime=%f, st_mtime=%f, st_ctime=%f)",
		s.mode, s.ino, s.dev, s.nlink, s.uid, s.gid, s.size, s.atime, s.mtime, s.ctime,
	)), nil
}

func makeStatMethod(sb *Sandbox, osMod *py.Module) *py.Method {
	m := py.MustNewMethod("stat", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
		if !sb.FileReadEnabled() {
			return nil, py.ExceptionNewf(py.PermissionError, "os.stat() is not allowed: file read is disabled")
		}
		if len(args) < 1 {
			return nil, py.ExceptionNewf(py.TypeError, "stat() requires at least 1 argument")
		}
		var path string
		switch p := args[0].(type) {
		case py.String:
			path = string(p)
		case py.Bytes:
			path = string(p)
		default:
			return nil, py.ExceptionNewf(py.TypeError, "stat() argument 1 must be str or bytes, not %s", args[0].Type().Name)
		}

		fi, err := os.Stat(path)
		if err != nil {
			return nil, py.ExceptionNewf(py.FileNotFoundError, "[Errno 2] No such file or directory: '%s'", path)
		}

		res := &statResult{
			size:  fi.Size(),
			mtime: float64(fi.ModTime().UnixNano()) / 1e9,
		}
		res.atime = res.mtime
		res.ctime = res.mtime

		if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
			res.mode = int64(sys.Mode)
			res.ino = int64(sys.Ino)
			res.dev = int64(sys.Dev)
			res.nlink = int64(sys.Nlink)
			res.uid = int64(sys.Uid)
			res.gid = int64(sys.Gid)
			res.size = int64(sys.Size)
			res.atime = float64(sys.Atim.Sec) + float64(sys.Atim.Nsec)/1e9
			res.mtime = float64(sys.Mtim.Sec) + float64(sys.Mtim.Nsec)/1e9
			res.ctime = float64(sys.Ctim.Sec) + float64(sys.Ctim.Nsec)/1e9
		} else {
			mode := int64(fi.Mode().Perm())
			if fi.IsDir() {
				mode |= 0o040000
			} else {
				mode |= 0o100000
			}
			res.mode = mode
		}
		return res, nil
	}, 0, "stat(path) -> stat_result\nPerform a stat system call on the given path.")
	m.Module = osMod
	return m
}

var walkIteratorType = py.NewType("walk_iterator", "Iterator for os.walk()")

type walkIterator struct {
	items []py.Object
	idx   int
}

func (w *walkIterator) Type() *py.Type { return walkIteratorType }
func (w *walkIterator) M__iter__() (py.Object, error) { return w, nil }
func (w *walkIterator) M__next__() (py.Object, error) {
	if w.idx >= len(w.items) {
		return nil, py.MakeException(py.StopIteration)
	}
	item := w.items[w.idx]
	w.idx++
	return item, nil
}

func makeWalkMethod(sb *Sandbox, osMod *py.Module) *py.Method {
	m := py.MustNewMethod("walk", func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
		if !sb.FileReadEnabled() {
			return nil, py.ExceptionNewf(py.PermissionError, "os.walk() is not allowed: file read is disabled")
		}
		if len(args) < 1 {
			return nil, py.ExceptionNewf(py.TypeError, "walk() requires at least 1 argument")
		}
		var top string
		switch p := args[0].(type) {
		case py.String:
			top = string(p)
		case py.Bytes:
			top = string(p)
		default:
			return nil, py.ExceptionNewf(py.TypeError, "walk() argument 1 must be str or bytes, not %s", args[0].Type().Name)
		}

		topdown := true
		if len(args) >= 2 {
			if b, ok := args[1].(py.Bool); ok {
				topdown = bool(b)
			}
		}
		if val, exists := kwargs["topdown"]; exists {
			if b, ok := val.(py.Bool); ok {
				topdown = bool(b)
			}
		}

		var items []py.Object
		var walkDir func(current string) error
		walkDir = func(current string) error {
			entries, err := os.ReadDir(current)
			if err != nil {
				return nil
			}
			var dirnames []py.Object
			var filenames []py.Object
			var subdirs []string
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() {
					dirnames = append(dirnames, py.String(name))
					subdirs = append(subdirs, filepath.Join(current, name))
				} else {
					filenames = append(filenames, py.String(name))
				}
			}

			currTuple := py.Tuple{
				py.String(current),
				py.NewListFromItems(dirnames),
				py.NewListFromItems(filenames),
			}

			if topdown {
				items = append(items, currTuple)
				for _, sub := range subdirs {
					if err := walkDir(sub); err != nil {
						return err
					}
				}
			} else {
				for _, sub := range subdirs {
					if err := walkDir(sub); err != nil {
						return err
					}
				}
				items = append(items, currTuple)
			}
			return nil
		}

		if err := walkDir(top); err != nil {
			return nil, err
		}

		return &walkIterator{items: items}, nil
	}, 0, "walk(top, topdown=True, onerror=None, followlinks=False) -> iterator\nDirectory tree generator.")
	m.Module = osMod
	return m
}

