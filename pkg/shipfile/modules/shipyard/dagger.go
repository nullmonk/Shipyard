package shipmod

import (
	"strings"
	"sync"

	"github.com/go-python/gpython/py"
)

// ContainerBuilder is the interface that routes Python shipfile calls to a
// container build system (e.g. Dagger). Defined here so pkg/shipfile has no
// direct dependency on dagger.io/dagger.
type ContainerBuilder interface {
	From(image, name string) error
	Use(name string) error
	Run(argv []string, stdin string) (stdout, stderr string, rc int, err error)
	// Copy copies srcPath into dst. fromContainer="" means copy from ContextBundle.
	Copy(srcPath, dst, fromContainer string) error
	WriteFile(path, content string) error
	Workdir(path string) error
	Env(key, value string) error
	User(name string) error
	// Dockerfile builds from an inline Dockerfile using the ContextBundle as the
	// build context. It takes over the build; commands after it operate on the
	// final image.
	Dockerfile(content string) error
	// Save records a file or directory to export to the artifacts output.
	// src may be "path" (active container) or "stage:path" (named stage).
	// dst is a relative path within the artifacts output directory.
	Save(src, dst string) error
}

var (
	activeBuilderMu sync.RWMutex
	activeBuilder   ContainerBuilder
)

// SetActiveBuilder makes b the active builder for Python builtin dispatch.
func SetActiveBuilder(b ContainerBuilder) {
	activeBuilderMu.Lock()
	defer activeBuilderMu.Unlock()
	activeBuilder = b
}

// ClearActiveBuilder removes the active builder.
func ClearActiveBuilder() {
	activeBuilderMu.Lock()
	defer activeBuilderMu.Unlock()
	activeBuilder = nil
}

// GetActiveBuilder returns the active builder, or nil.
func GetActiveBuilder() ContainerBuilder {
	activeBuilderMu.RLock()
	defer activeBuilderMu.RUnlock()
	return activeBuilder
}

func requireBuilder(fn string) (ContainerBuilder, error) {
	b := GetActiveBuilder()
	if b == nil {
		return nil, py.ExceptionNewf(py.RuntimeError,
			"%s is only available inside a Builder's build() method", fn)
	}
	return b, nil
}

// parseCopySrc splits a src argument of the form "container:path" or just "path".
// Returns (containerName, path). If no colon is present, containerName is "".
func parseCopySrc(src string) (container, path string) {
	if i := strings.IndexByte(src, ':'); i > 0 {
		return src[:i], src[i+1:]
	}
	return "", src
}

// From(image) / From(image, "name")
// A second From() call without a name is an error — stages must be named.
func fromFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "From() requires an image argument")
	}
	image, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "From() image must be a string")
	}
	name := ""
	if len(args) >= 2 {
		if s, ok := args[1].(py.String); ok {
			name = string(s)
		}
	}
	if n, ok := kwargs["name"]; ok {
		if s, ok := n.(py.String); ok {
			name = string(s)
		}
	}
	b, err := requireBuilder("From()")
	if err != nil {
		return nil, err
	}
	if err := b.From(string(image), name); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "From(%q): %v", string(image), err)
	}
	return py.None, nil
}

// Use("name") — switch the active container context back to a named stage.
func useFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "Use() requires a container name")
	}
	name, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Use() name must be a string")
	}
	b, err := requireBuilder("Use()")
	if err != nil {
		return nil, err
	}
	if err := b.Use(string(name)); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "Use(%q): %v", string(name), err)
	}
	return py.None, nil
}

// Copy(src, dst)
//
// src may be:
//
//	"path"           copy path from the ContextBundle (upstream source)
//	"."              copy everything from the ContextBundle
//	"stage:path"     copy path from a named container stage
//	"stage:."        copy everything from a named container stage
func copyFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "Copy() requires at least a src argument")
	}
	srcArg, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Copy() src must be a string")
	}
	dst := "."
	if len(args) >= 2 {
		dstArg, ok := args[1].(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Copy() dst must be a string")
		}
		dst = string(dstArg)
	}
	fromContainer, srcPath := parseCopySrc(string(srcArg))
	b, err := requireBuilder("Copy()")
	if err != nil {
		return nil, err
	}
	if err := b.Copy(srcPath, dst, fromContainer); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "Copy(%q, %q): %v", string(srcArg), dst, err)
	}
	return py.None, nil
}

// Write(path, content) — write a file into the active container.
func writeFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 2 {
		return nil, py.ExceptionNewf(py.TypeError, "Write() requires path and content arguments")
	}
	path, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Write() path must be a string")
	}
	var content string
	switch v := args[1].(type) {
	case py.String:
		content = string(v)
	case py.Bytes:
		content = string(v)
	default:
		return nil, py.ExceptionNewf(py.TypeError, "Write() content must be a string or bytes")
	}
	b, err := requireBuilder("Write()")
	if err != nil {
		return nil, err
	}
	if err := b.WriteFile(string(path), content); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "Write(%q): %v", string(path), err)
	}
	return py.None, nil
}

// Workdir(path) — set the working directory in the active container.
func workdirFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "Workdir() requires a path argument")
	}
	path, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Workdir() path must be a string")
	}
	b, err := requireBuilder("Workdir()")
	if err != nil {
		return nil, err
	}
	if err := b.Workdir(string(path)); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "Workdir(%q): %v", string(path), err)
	}
	return py.None, nil
}

// Script("""...""") — run a multiline shell script in the active container or on host.
// Equivalent to Run(["/bin/sh"], stdin=script). Returns a RunResult.
func scriptFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "Script() requires a script argument")
	}
	var content string
	switch v := args[0].(type) {
	case py.String:
		content = string(v)
	case py.Bytes:
		content = string(v)
	default:
		return nil, py.ExceptionNewf(py.TypeError, "Script() argument must be a string or bytes")
	}

	// Recording mode: capture as a single entry.
	runRecorderMu.Lock()
	if runRecording {
		runRecorded = append(runRecorded, "/bin/sh\x00"+content)
		runRecorderMu.Unlock()
		return &RunResult{}, nil
	}
	runRecorderMu.Unlock()

	interp := "/bin/sh"
	if i, ok := kwargs["interp"]; ok {
		if s, ok := i.(py.String); ok && string(s) != "" {
			interp = string(s)
		}
	} else {
		firstLine := strings.SplitN(content, "\n", 2)[0]
		firstLine = strings.TrimRight(firstLine, "\r")
		if strings.HasPrefix(firstLine, "#!") {
			shebang := strings.TrimSpace(strings.TrimPrefix(firstLine, "#!"))
			if shebang != "" {
				interp = shebang
			}
		}
	}
	argv := strings.Fields(interp)
	if len(argv) == 0 {
		argv = []string{"/bin/sh"}
	}

	displayCmd := ""
	for _, line := range strings.Split(content, "\n") {
		if s := strings.TrimSpace(line); s != "" && !strings.HasPrefix(s, "#") {
			displayCmd = s + " …"
			break
		}
	}
	if displayCmd == "" {
		displayCmd = "<script>"
	}

	b := GetActiveBuilder()
	if b != nil {
		stdout, stderr, rc, runErr := b.Run(argv, content)
		if runErr != nil {
			return nil, py.ExceptionNewf(py.RuntimeError, "Script(): %v", runErr)
		}
		check := true
		if c, ok := kwargs["check"]; ok {
			check = (c != py.False && c != py.None)
		}
		if check && rc != 0 {
			return nil, py.ExceptionNewf(py.RuntimeError, "Script() failed with exit code %d\n%s", rc, stderr)
		}
		return &RunResult{Stdout: stdout, Stderr: stderr, ReturnCode: rc}, nil
	}

	mod, _ := self.(*py.Module)
	var ctx py.Context
	if mod != nil {
		ctx = mod.Context
	}
	return executeHostCommand(ctx, argv, displayCmd, strings.NewReader(content), "", kwargs, "Script")
}

// Env(key, value) — set an environment variable in the active container.
func envFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 2 {
		return nil, py.ExceptionNewf(py.TypeError, "Env() requires key and value arguments")
	}
	key, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Env() key must be a string")
	}
	val, ok := args[1].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Env() value must be a string")
	}
	b, err := requireBuilder("Env()")
	if err != nil {
		return nil, err
	}
	if err := b.Env(string(key), string(val)); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "Env(%q): %v", string(key), err)
	}
	return py.None, nil
}

// Save(src, dst) — record a file/directory for export to the artifacts output.
//
// src may be:
//
//	"path"         export from the active container
//	"stage:path"   export from a named stage
//
// dst is a relative path within the artifacts output directory.
func saveFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 2 {
		return nil, py.ExceptionNewf(py.TypeError, "Save() requires src and dst arguments")
	}
	src, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Save() src must be a string")
	}
	dst, ok := args[1].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Save() dst must be a string")
	}
	b, err := requireBuilder("Save()")
	if err != nil {
		return nil, err
	}
	if err := b.Save(string(src), string(dst)); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "Save(%q, %q): %v", string(src), string(dst), err)
	}
	return py.None, nil
}

// Dockerfile("""...""") — build from an inline Dockerfile. Uses the ContextBundle
// as the build context. Takes over the build; commands before it are not allowed.
func dockerfileFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "Dockerfile() requires a content argument")
	}
	content, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "Dockerfile() content must be a string")
	}
	b, err := requireBuilder("Dockerfile()")
	if err != nil {
		return nil, err
	}
	if err := b.Dockerfile(string(content)); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "Dockerfile(): %v", err)
	}
	return py.None, nil
}

// User(name) — set the user for subsequent RUN instructions.
func userFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "User() requires a name argument")
	}
	name, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "User() name must be a string")
	}
	b, err := requireBuilder("User()")
	if err != nil {
		return nil, err
	}
	if err := b.User(string(name)); err != nil {
		return nil, py.ExceptionNewf(py.RuntimeError, "User(%q): %v", string(name), err)
	}
	return py.None, nil
}
