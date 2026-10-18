package shipmod

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/go-python/gpython/py"
)

// RunDepthEnv is the environment variable automatically set and incremented
// on each shipyard.Run() subprocess invocation.
const (
	RunDepthEnv = "SHIPYARD_DEPTH"
	MaxRunDepth = 10
)

func envWithIncrementedDepth() ([]string, error) {
	depth := 0
	if v := os.Getenv(RunDepthEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			depth = n
		}
	}
	if depth >= MaxRunDepth {
		return nil, py.ExceptionNewf(py.RuntimeError,
			"maximum shipyard.Run() recursion depth (%d) exceeded", MaxRunDepth)
	}
	base := os.Environ()
	out := make([]string, 0, len(base)+1)
	prefix := RunDepthEnv + "="
	for _, kv := range base {
		if strings.HasPrefix(kv, prefix) || strings.HasPrefix(kv, "SHIPYARD_IN_CONTAINER=") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, fmt.Sprintf("%s=%d", RunDepthEnv, depth+1))
	return out, nil
}

var (
	executionCheckerMu sync.RWMutex
	executionChecker   func(py.Context) bool
)

var (
	runRecorderMu sync.Mutex
	runRecorded   []string
	runRecording  bool
)

// StartRecording enables recording mode: subsequent Run() calls are captured
// rather than executed and return an empty RunResult.
func StartRecording() {
	runRecorderMu.Lock()
	defer runRecorderMu.Unlock()
	runRecorded = nil
	runRecording = true
}

// StopRecording disables recording mode and returns the captured entries.
// Each entry is "cmd" or "cmd\x00stdin" for calls that included stdin.
func StopRecording() []string {
	runRecorderMu.Lock()
	defer runRecorderMu.Unlock()
	runRecording = false
	out := make([]string, len(runRecorded))
	copy(out, runRecorded)
	runRecorded = nil
	return out
}

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

	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "Run() requires a cmd argument")
	}

	var argv []string
	var displayCmd string
	switch v := args[0].(type) {
	case py.String:
		displayCmd = string(v)
		argv = []string{"/bin/sh", "-c", string(v)}
	case py.Tuple:
		for _, item := range v {
			s, ok := item.(py.String)
			if !ok {
				return nil, py.ExceptionNewf(py.TypeError, "Run() cmd elements must be strings")
			}
			argv = append(argv, string(s))
		}
		displayCmd = strings.Join(argv, " ")
	case *py.List:
		for _, item := range v.Items {
			s, ok := item.(py.String)
			if !ok {
				return nil, py.ExceptionNewf(py.TypeError, "Run() cmd elements must be strings")
			}
			argv = append(argv, string(s))
		}
		displayCmd = strings.Join(argv, " ")
	default:
		return nil, py.ExceptionNewf(py.TypeError, "Run() cmd must be a string, list, or tuple")
	}

	if len(argv) == 0 {
		return nil, py.ExceptionNewf(py.ValueError, "Run() cmd must not be empty")
	}

	var stdinReader io.Reader
	var stdinStr string
	stdinDisplay := ""
	parseStdin := func(obj py.Object) error {
		switch s := obj.(type) {
		case py.String:
			if len(s) > 0 {
				stdinReader = strings.NewReader(string(s))
				stdinStr = string(s)
				stdinDisplay = string(s)
			}
		case py.Bytes:
			stdinReader = bytes.NewReader([]byte(s))
			stdinStr = string(s)
			stdinDisplay = fmt.Sprintf("<stdin: %d bytes>", len(s))
		default:
			return py.ExceptionNewf(py.TypeError, "Run() stdin must be a string or bytes")
		}
		return nil
	}
	if len(args) >= 2 {
		if err := parseStdin(args[1]); err != nil {
			return nil, err
		}
	} else if s, ok := kwargs["stdin"]; ok {
		if err := parseStdin(s); err != nil {
			return nil, err
		}
	}

	// Recording mode: capture commands without executing (for SetupHash).
	runRecorderMu.Lock()
	if runRecording {
		entry := displayCmd
		if stdinDisplay != "" {
			entry += "\x00" + stdinDisplay
		}
		runRecorded = append(runRecorded, entry)
		runRecorderMu.Unlock()
		return &RunResult{}, nil
	}
	runRecorderMu.Unlock()

	// Dagger mode: route through the active ContainerBuilder (host-side Builder builds).
	if b := GetActiveBuilder(); b != nil {
		stdout, stderr, rc, runErr := b.Run(argv, stdinStr)
		if runErr != nil {
			return nil, py.ExceptionNewf(py.RuntimeError, "Run(%q): %v", displayCmd, runErr)
		}
		check := true
		if c, ok := kwargs["check"]; ok {
			check = (c != py.False && c != py.None)
		}
		if check && rc != 0 {
			return nil, py.ExceptionNewf(py.RuntimeError,
				"Command %q failed with exit code %d\n%s", displayCmd, rc, stderr)
		}
		return &RunResult{Stdout: stdout, Stderr: stderr, ReturnCode: rc}, nil
	}

	return executeHostCommand(mod.Context, argv, displayCmd, stdinReader, stdinDisplay, kwargs, "Run")
}

func executeHostCommand(ctx py.Context, argv []string, displayCmd string, stdinReader io.Reader, stdinDisplay string, kwargs py.StringDict, opName string) (*RunResult, error) {
	if !isExecutionAllowed(ctx) {
		return nil, py.ExceptionNewf(py.PermissionError,
			"shipyard.%s() is only available during build and test phases (setup/build/test)", opName)
	}

	env, err := envWithIncrementedDepth()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	if stdinReader != nil {
		cmd.Stdin = stdinReader
	}
	var stdout, stderr bytes.Buffer
	quiet := false
	if q, ok := kwargs["quiet"]; ok && q != py.False && q != py.None {
		quiet = true
	}
	verbose := os.Getenv("SHIPYARD_VERBOSE") != ""

	if !quiet {
		fmt.Printf("$ %s\n", displayCmd)
		if stdinDisplay != "" {
			if strings.HasSuffix(stdinDisplay, "\n") {
				fmt.Print(stdinDisplay)
			} else {
				fmt.Println(stdinDisplay)
			}
		}
		cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	err = cmd.Run()
	rc := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else {
			return nil, py.ExceptionNewf(py.OSError, "%s() failed to start %q: %v", opName, argv[0], err)
		}
	}

	check := true
	if c, ok := kwargs["check"]; ok {
		check = (c != py.False && c != py.None)
	}
	failed := check && rc != 0
	if quiet && (failed || verbose) {
		if s := stdout.String(); s != "" {
			fmt.Print(s)
		}
		if s := stderr.String(); s != "" {
			fmt.Fprint(os.Stderr, s)
		}
	}
	if failed {
		return nil, py.ExceptionNewf(py.RuntimeError, "Command %q failed with exit code %d", argv[0], rc)
	}

	return &RunResult{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ReturnCode: rc,
	}, nil
}
