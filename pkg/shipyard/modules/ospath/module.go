// Package ospath implements the Python "os.path" (and "posixpath") module for gpython.
package ospath

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/go-python/gpython/py"
)

// FileReadChecker is a function that checks if file read is allowed for a given context.
var FileReadChecker func(ctx py.Context) bool

// SetFileReadChecker configures the callback used to check file read permissions.
func SetFileReadChecker(fn func(ctx py.Context) bool) {
	FileReadChecker = fn
}

func checkFileRead(self py.Object) error {
	if FileReadChecker == nil {
		return nil
	}
	if mod, ok := self.(*py.Module); ok && mod.Context != nil {
		if !FileReadChecker(mod.Context) {
			return py.ExceptionNewf(py.PermissionError, "file read is disabled in this phase")
		}
	}
	return nil
}

func getPathArg(args py.Tuple, idx int, name string) (string, error) {
	if idx >= len(args) {
		return "", py.ExceptionNewf(py.TypeError, "%s() requires at least %d arguments", name, idx+1)
	}
	switch s := args[idx].(type) {
	case py.String:
		return string(s), nil
	case py.Bytes:
		return string(s), nil
	default:
		return "", py.ExceptionNewf(py.TypeError, "%s() argument %d must be str or bytes, not %s", name, idx+1, args[idx].Type().Name)
	}
}

func pathJoin(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) == 0 {
		return py.String(""), nil
	}
	parts := make([]string, len(args))
	for i := range args {
		p, err := getPathArg(args, i, "join")
		if err != nil {
			return nil, err
		}
		parts[i] = p
	}
	res := filepath.Join(parts...)
	// In Python os.path.join, if the last arg ends with a separator, preserve it
	last := parts[len(parts)-1]
	if (strings.HasSuffix(last, "/") || strings.HasSuffix(last, "\\")) && !strings.HasSuffix(res, "/") && !strings.HasSuffix(res, "\\") {
		res += "/"
	}
	return py.String(res), nil
}

func pathSplit(self py.Object, args py.Tuple) (py.Object, error) {
	p, err := getPathArg(args, 0, "split")
	if err != nil {
		return nil, err
	}
	dir, file := filepath.Split(p)
	if len(dir) > 1 && (strings.HasSuffix(dir, "/") || strings.HasSuffix(dir, "\\")) {
		dir = dir[:len(dir)-1]
	}
	return py.Tuple{py.String(dir), py.String(file)}, nil
}

func pathSplitext(self py.Object, args py.Tuple) (py.Object, error) {
	p, err := getPathArg(args, 0, "splitext")
	if err != nil {
		return nil, err
	}
	ext := filepath.Ext(p)
	root := p[:len(p)-len(ext)]
	return py.Tuple{py.String(root), py.String(ext)}, nil
}

func pathDirname(self py.Object, args py.Tuple) (py.Object, error) {
	p, err := getPathArg(args, 0, "dirname")
	if err != nil {
		return nil, err
	}
	dir, _ := filepath.Split(p)
	if len(dir) > 1 && (strings.HasSuffix(dir, "/") || strings.HasSuffix(dir, "\\")) {
		dir = dir[:len(dir)-1]
	}
	return py.String(dir), nil
}

func pathBasename(self py.Object, args py.Tuple) (py.Object, error) {
	p, err := getPathArg(args, 0, "basename")
	if err != nil {
		return nil, err
	}
	_, file := filepath.Split(p)
	return py.String(file), nil
}

func pathNormpath(self py.Object, args py.Tuple) (py.Object, error) {
	p, err := getPathArg(args, 0, "normpath")
	if err != nil {
		return nil, err
	}
	return py.String(filepath.Clean(p)), nil
}

func pathAbspath(self py.Object, args py.Tuple) (py.Object, error) {
	p, err := getPathArg(args, 0, "abspath")
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, py.ExceptionNewf(py.OSError, "%v", err)
	}
	return py.String(abs), nil
}

func pathIsabs(self py.Object, args py.Tuple) (py.Object, error) {
	p, err := getPathArg(args, 0, "isabs")
	if err != nil {
		return nil, err
	}
	if filepath.IsAbs(p) {
		return py.True, nil
	}
	return py.False, nil
}

func pathExists(self py.Object, args py.Tuple) (py.Object, error) {
	if err := checkFileRead(self); err != nil {
		return nil, err
	}
	p, err := getPathArg(args, 0, "exists")
	if err != nil {
		return nil, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return py.True, nil
	}
	return py.False, nil
}

func pathIsfile(self py.Object, args py.Tuple) (py.Object, error) {
	if err := checkFileRead(self); err != nil {
		return nil, err
	}
	p, err := getPathArg(args, 0, "isfile")
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(p)
	if err == nil && !fi.IsDir() {
		return py.True, nil
	}
	return py.False, nil
}

func pathIsdir(self py.Object, args py.Tuple) (py.Object, error) {
	if err := checkFileRead(self); err != nil {
		return nil, err
	}
	p, err := getPathArg(args, 0, "isdir")
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(p)
	if err == nil && fi.IsDir() {
		return py.True, nil
	}
	return py.False, nil
}

func pathGetsize(self py.Object, args py.Tuple) (py.Object, error) {
	if err := checkFileRead(self); err != nil {
		return nil, err
	}
	p, err := getPathArg(args, 0, "getsize")
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, py.ExceptionNewf(py.FileNotFoundError, "[Errno 2] No such file or directory: '%s'", p)
	}
	return py.Int(fi.Size()), nil
}

func makeMethods() []*py.Method {
	return []*py.Method{
		py.MustNewMethod("join", pathJoin, 0, "join(a, *p) -> str\nJoin one or more path components intelligently."),
		py.MustNewMethod("split", pathSplit, 0, "split(p) -> (head, tail)\nSplit a pathname into (head, tail)."),
		py.MustNewMethod("splitext", pathSplitext, 0, "splitext(p) -> (root, ext)\nSplit the extension from a pathname."),
		py.MustNewMethod("dirname", pathDirname, 0, "dirname(p) -> str\nReturns the directory component of a pathname"),
		py.MustNewMethod("basename", pathBasename, 0, "basename(p) -> str\nReturns the final component of a pathname"),
		py.MustNewMethod("normpath", pathNormpath, 0, "normpath(p) -> str\nNormalize a pathname."),
		py.MustNewMethod("abspath", pathAbspath, 0, "abspath(path) -> str\nReturn an absolute path."),
		py.MustNewMethod("isabs", pathIsabs, 0, "isabs(s) -> bool\nTest whether a path is absolute."),
		py.MustNewMethod("exists", pathExists, 0, "exists(path) -> bool\nTest whether a path exists."),
		py.MustNewMethod("isfile", pathIsfile, 0, "isfile(path) -> bool\nTest whether a path is a regular file."),
		py.MustNewMethod("isdir", pathIsdir, 0, "isdir(path) -> bool\nReturn true if the pathname refers to an existing directory."),
		py.MustNewMethod("getsize", pathGetsize, 0, "getsize(filename) -> int\nReturn the size of a file, reported by os.stat()."),
	}
}

func makeGlobals() py.StringDict {
	return py.StringDict{
		"sep":     py.String("/"),
		"altsep":  py.None,
		"pathsep": py.String(":"),
		"defpath": py.String(":/bin:/usr/bin"),
		"devnull": py.String("/dev/null"),
		"curdir":  py.String("."),
		"pardir":  py.String(".."),
		"extsep":  py.String("."),
	}
}

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "os.path",
			Doc:  "Common pathname manipulations, POSIX style.",
		},
		Methods: makeMethods(),
		Globals: makeGlobals(),
	})

	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "posixpath",
			Doc:  "Common operations on POSIX pathnames.",
		},
		Methods: makeMethods(),
		Globals: makeGlobals(),
	})
}
