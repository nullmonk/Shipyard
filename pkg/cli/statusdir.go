package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file implements the `--status-dir <tmpdir>` worker protocol used when
// the Drydock server spawns `shipyard build` as a detached child process.
// The server creates tmpdir under its own work root, spawns:
//
//	shipyard build -f <tmpdir>/shipfile.py [...] --status-dir <tmpdir>
//
// disowned (no pipes, Setsid), then polls the files below for live tail,
// completion, and cancellation (SIGKILL by PID). Layout:
//
//	<tmpdir>/status.json     {state, pid, startedAt, exitCode, error}
//	<tmpdir>/build.log       combined stdout+stderr of the whole build
//	<tmpdir>/artifacts/      exported build outputs (build --output forced here)
//	<tmpdir>/artifacts.json  {artifacts: [<relpath>, ...]} written on success
//
// States are "running", "succeeded", and "failed". The server treats a dead
// PID with no terminal status.json as a lost worker (requeue-once, else
// failed); a `cancel` file in tmpdir is a cooperative stop request honored
// by in-process simulators (real `shipyard build` is stopped via SIGKILL,
// which needs no cooperation).
//
// The protocol is transport-agnostic on purpose: anything that can write
// these files (including the Drydock fake-build simulator used in tests)
// can stand in for the shipyard binary.

// WorkerStatus is the status.json schema shared with the Drydock server.
type WorkerStatus struct {
	State     string `json:"state"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
	ExitCode  int    `json:"exit_code"`
	Error     string `json:"error,omitempty"`
}

// Worker state values for WorkerStatus.State.
const (
	WorkerStateRunning   = "running"
	WorkerStateSucceeded = "succeeded"
	WorkerStateFailed    = "failed"
)

// WorkerResult is the artifacts.json schema: artifact paths relative to
// <tmpdir>/artifacts/, slash-separated.
type WorkerResult struct {
	Artifacts []string `json:"artifacts"`
}

// WorkerArtifactsDir is the fixed subdirectory of a status dir that holds
// exported build outputs.
const WorkerArtifactsDir = "artifacts"

// writeWorkerStatus atomically replaces <dir>/status.json (write temp +
// rename so polls never see a torn file).
func writeWorkerStatus(dir string, st WorkerStatus) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "status-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, "status.json"))
}

// collectWorkerArtifacts lists every file under <dir>/artifacts, relative
// to it, slash-separated and sorted by walk order.
func collectWorkerArtifacts(dir string) []string {
	root := filepath.Join(dir, WorkerArtifactsDir)
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, rErr := filepath.Rel(root, path)
		if rErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out
}

// superviseBuild runs fn with stdout/stderr tee'd to <statusDir>/build.log,
// maintains <statusDir>/status.json (running -> terminal), and writes
// <statusDir>/artifacts.json on success. It forces nothing about fn's own
// output layout; the caller (runBuild) points --output at
// <statusDir>/artifacts before invoking fn.
func superviseBuild(statusDir string, fn func() error) error {
	if err := os.MkdirAll(filepath.Join(statusDir, WorkerArtifactsDir), 0o755); err != nil {
		return fmt.Errorf("status-dir: %w", err)
	}
	logPath := filepath.Join(statusDir, "build.log")
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("status-dir log: %w", err)
	}

	started := time.Now().UTC()
	if err := writeWorkerStatus(statusDir, WorkerStatus{
		State:     WorkerStateRunning,
		PID:       os.Getpid(),
		StartedAt: started.Format(time.RFC3339),
	}); err != nil {
		_ = logF.Close()
		return fmt.Errorf("status-dir status: %w", err)
	}

	// Capture everything the build prints — including library stdout — by
	// redirecting the process-wide descriptors through pipes that tee to
	// both the original destination and the log file.
	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = logF.Close()
		return err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = logF.Close()
		return err
	}
	os.Stdout, os.Stderr = outW, errW
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.MultiWriter(origOut, logF), outR)
	}()
	doneErr := make(chan struct{})
	go func() {
		defer close(doneErr)
		_, _ = io.Copy(io.MultiWriter(origErr, logF), errR)
	}()

	runErr := fn()

	_ = outW.Close()
	_ = errW.Close()
	os.Stdout, os.Stderr = origOut, origErr
	<-done
	<-doneErr
	_ = logF.Close()

	st := WorkerStatus{
		State:     WorkerStateSucceeded,
		PID:       os.Getpid(),
		StartedAt: started.Format(time.RFC3339),
	}
	var arts []string
	if runErr != nil {
		st.State = WorkerStateFailed
		st.ExitCode = 1
		st.Error = strings.TrimSpace(runErr.Error())
	} else {
		arts = collectWorkerArtifacts(statusDir)
		raw, mErr := json.MarshalIndent(WorkerResult{Artifacts: arts}, "", "  ")
		if mErr == nil {
			_ = os.WriteFile(filepath.Join(statusDir, "artifacts.json"), append(raw, '\n'), 0o644)
		}
	}
	if wErr := writeWorkerStatus(statusDir, st); wErr != nil {
		fmt.Fprintf(origErr, "warning: status-dir final status: %v\n", wErr)
	}
	return runErr
}
