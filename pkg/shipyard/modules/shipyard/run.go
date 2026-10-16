package shipmod

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/go-python/gpython/py"
)

var (
	executionCheckerMu sync.RWMutex
	executionChecker   func(py.Context) bool
)

// SetExecutionChecker registers the Go callback used to authorize Run().
func SetExecutionChecker(fn func(py.Context) bool) {
	executionCheckerMu.Lock()
	defer executionCheckerMu.Unlock()
	executionChecker = fn
}

func isExecutionAllowed(ctx py.Context) bool {
	executionCheckerMu.RLock()
	defer executionCheckerMu.RUnlock()
	if executionChecker == nil {
		return false
	}
	return executionChecker(ctx)
}

var runResultType = py.NewType("RunResult", "Result of shipyard.Run()")

type RunResult struct {
	Stdout     string
	Stderr     string
	ReturnCode int
}

func (r *RunResult) Type() *py.Type { return runResultType }

func (r *RunResult) M__bool__() (py.Object, error) {
	if r.ReturnCode == 0 {
		return py.True, nil
	}
	return py.False, nil
}

func (r *RunResult) M__repr__() (py.Object, error) {
	return py.String(fmt.Sprintf(
		"RunResult(returncode=%d, stdout=%q, stderr=%q)",
		r.ReturnCode, r.Stdout, r.Stderr,
	)), nil
}

func (r *RunResult) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "stdout":
		return py.String(r.Stdout), nil
	case "stderr":
		return py.String(r.Stderr), nil
	case "returncode":
		return py.Int(r.ReturnCode), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'RunResult' object has no attribute '%s'", name)
}

// runFn is shipyard.Run(cmd, stdin="").
// It checks _run_enabled in the module globals before executing anything.
func runFn(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	mod, ok := self.(*py.Module)
	if !ok {
		return nil, py.ExceptionNewf(py.SystemError, "Run() called with unexpected self type")
	}
	if !isExecutionAllowed(mod.Context) {
		return nil, py.ExceptionNewf(py.PermissionError,
			"shipyard.Run() is only available during build phases (setup/build)")
	}

	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "Run() requires a cmd argument")
	}

	var argv []string
	switch v := args[0].(type) {
	case py.String:
		argv = []string{"/bin/sh", "-c", string(v)}
	case py.Tuple:
		for _, item := range v {
			s, ok := item.(py.String)
			if !ok {
				return nil, py.ExceptionNewf(py.TypeError, "Run() cmd elements must be strings")
			}
			argv = append(argv, string(s))
		}
	case *py.List:
		for _, item := range v.Items {
			s, ok := item.(py.String)
			if !ok {
				return nil, py.ExceptionNewf(py.TypeError, "Run() cmd elements must be strings")
			}
			argv = append(argv, string(s))
		}
	default:
		return nil, py.ExceptionNewf(py.TypeError, "Run() cmd must be a string, list, or tuple")
	}

	if len(argv) == 0 {
		return nil, py.ExceptionNewf(py.ValueError, "Run() cmd must not be empty")
	}

	stdinStr := ""
	if len(args) >= 2 {
		s, ok := args[1].(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Run() stdin must be a string")
		}
		stdinStr = string(s)
	} else if s, ok := kwargs["stdin"]; ok {
		str, ok := s.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "Run() stdin must be a string")
		}
		stdinStr = string(str)
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	if stdinStr != "" {
		cmd.Stdin = strings.NewReader(stdinStr)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	rc := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else {
			return nil, py.ExceptionNewf(py.OSError, "Run() failed to start %q: %v", argv[0], err)
		}
	}

	return &RunResult{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ReturnCode: rc,
	}, nil
}
