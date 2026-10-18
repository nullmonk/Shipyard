package builder

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	shipmod "shipyard/pkg/shipfile/modules/shipyard"
	"shipyard/pkg/transfer"
)

// LocalBuilder implements shipmod.ContainerBuilder for host-local builds (--local).
// In a local build, From/Use/User/Env/Dockerfile are no-ops or set environment variables,
// Copy/WriteFile/Workdir operate directly on the local filesystem, and Run executes
// commands directly on the host/container environment.
type LocalBuilder struct {
	workdir string
	outDir  string
	out     io.Writer
	cmdOut  io.Writer
	cmdErr  io.Writer
	verbose bool
	Saves   [][2]string
}

var _ shipmod.ContainerBuilder = (*LocalBuilder)(nil)

func NewLocalBuilder(outDir string, verbose bool) *LocalBuilder {
	cwd, _ := os.Getwd()
	return &LocalBuilder{
		workdir: cwd,
		outDir:  outDir,
		out:     os.Stdout,
		cmdOut:  os.Stdout,
		cmdErr:  os.Stderr,
		verbose: verbose,
	}
}

func (l *LocalBuilder) From(image, name string) error {
	if l.verbose {
		fmt.Fprintf(l.out, "[*] Local build: using current environment (ignoring FROM %s)\n", image)
	}
	return nil
}

func (l *LocalBuilder) Use(name string) error {
	return nil
}

func (l *LocalBuilder) Run(argv []string, stdin string) (stdout, stderr string, rc int, err error) {
	var displayCmd string
	if len(argv) == 3 && shipmod.IsShell(argv[0]) && argv[1] == "-c" {
		displayCmd = argv[2]
	} else if stdin != "" && len(argv) > 0 && shipmod.IsShell(argv[0]) {
		displayCmd = shipmod.FormatScriptDisplayCmd(stdin)
	} else {
		displayCmd = strings.Join(argv, " ")
	}
	fmt.Fprintf(l.out, "    $ %s\n", truncate(displayCmd, 72))

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = l.workdir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = io.MultiWriter(l.cmdOut, &outBuf)
	cmd.Stderr = io.MultiWriter(l.cmdErr, &errBuf)

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		fmt.Fprintln(l.out, "    FAILED")
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			return outBuf.String(), errBuf.String(), exitCode, nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

func copyPathLocal(src, dst string) error {
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if absSrc == absDst {
		return nil
	}

	fi, err := os.Stat(absSrc)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return filepath.Walk(absSrc, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(absSrc, path)
			if err != nil {
				return err
			}
			target := filepath.Join(absDst, rel)
			absTarget, _ := filepath.Abs(target)
			if absSrc == absTarget || path == absTarget {
				return nil
			}
			if info.IsDir() {
				return os.MkdirAll(target, 0755)
			}
			return copyFileLocal(path, target)
		})
	}
	return copyFileLocal(absSrc, absDst)
}

func copyFileLocal(src, dst string) error {
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if absSrc == absDst {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(absDst), 0755); err != nil {
		return err
	}
	in, err := os.Open(absSrc)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(absDst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	fi, err := in.Stat()
	if err == nil {
		_ = os.Chmod(absDst, fi.Mode())
	}
	return nil
}

type localTransferDriver struct {
	l *LocalBuilder
}

func (l *LocalBuilder) transferContext() *transfer.Context {
	return transfer.NewContext(
		&localTransferDriver{l: l},
		transfer.ContainerCurrent,
		transfer.ContainerContext,
		transfer.ContainerArtifacts,
	)
}

func (drv *localTransferDriver) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(drv.l.workdir, p)
}

func (drv *localTransferDriver) ExpandSource(container, srcPath string) ([]string, bool, error) {
	absSrc := drv.resolvePath(srcPath)

	isGlob := strings.ContainsAny(srcPath, "*?[]")
	if isGlob {
		matches, err := filepath.Glob(absSrc)
		if err != nil {
			return nil, true, err
		}
		if len(matches) == 0 {
			return nil, true, fmt.Errorf("no files matched pattern %q", srcPath)
		}
		return matches, true, nil
	}

	fi, err := os.Stat(absSrc)
	if err != nil {
		return nil, false, err
	}
	if fi.IsDir() && (srcPath == "." || srcPath == "" || strings.HasSuffix(srcPath, "/.") || strings.HasSuffix(srcPath, string(filepath.Separator)+".")) {
		entries, err := os.ReadDir(absSrc)
		if err != nil {
			return nil, false, err
		}
		var result []string
		for _, e := range entries {
			result = append(result, filepath.Join(absSrc, e.Name()))
		}
		return result, true, nil
	}

	return []string{absSrc}, false, nil
}

func (drv *localTransferDriver) IsDir(container, path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}

func (drv *localTransferDriver) IsDirTarget(container, dstPath string) (bool, error) {
	if strings.HasSuffix(dstPath, "/") || strings.HasSuffix(dstPath, string(filepath.Separator)) || dstPath == "." || dstPath == "" {
		return true, nil
	}
	target := dstPath
	if !filepath.IsAbs(target) {
		if container == transfer.ContainerArtifacts && drv.l.outDir != "" {
			target = filepath.Join(drv.l.outDir, dstPath)
		} else {
			target = filepath.Join(drv.l.workdir, dstPath)
		}
	}
	fi, err := os.Stat(target)
	if err == nil && fi.IsDir() {
		return true, nil
	}
	return false, nil
}

func (drv *localTransferDriver) RunTasks(tasks []transfer.Task) error {
	l := drv.l
	for _, task := range tasks {
		if task.DstContainer == transfer.ContainerArtifacts {
			l.Saves = append(l.Saves, [2]string{task.SrcPath, task.DstPath})
			if l.outDir == "" {
				continue
			}
			dstPath := filepath.Join(l.outDir, task.DstPath)
			if err := copyPathLocal(task.SrcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		dstPath := task.DstPath
		if !filepath.IsAbs(dstPath) {
			dstPath = filepath.Join(l.workdir, dstPath)
		}
		if err := copyPathLocal(task.SrcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func (l *LocalBuilder) Copy(srcPath string, dstAndFrom ...string) error {
	src := srcPath
	var dstArgs []string
	if len(dstAndFrom) >= 2 && dstAndFrom[1] != "" && !strings.Contains(src, ":") {
		src = dstAndFrom[1] + ":" + src
		dstArgs = []string{dstAndFrom[0]}
	} else if len(dstAndFrom) >= 1 {
		dstArgs = []string{dstAndFrom[0]}
	}
	return l.transferContext().Copy(src, dstArgs...)
}

func (l *LocalBuilder) WriteFile(path, content string) error {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(l.workdir, p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0644)
}

func (l *LocalBuilder) Workdir(path string) error {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(l.workdir, p)
	}
	if err := os.MkdirAll(p, 0755); err != nil {
		return err
	}
	l.workdir = p
	return os.Chdir(p)
}

func (l *LocalBuilder) Env(key, value string) error {
	return os.Setenv(key, value)
}

func (l *LocalBuilder) User(name string) error {
	return nil
}

func (l *LocalBuilder) Dockerfile(content string) error {
	return nil
}

// Save records a file, directory, or glob pattern for export to the artifacts container.
// Note: The artifacts output is a "container" with a raw layer for the files and custom description/title.
func (l *LocalBuilder) Save(src string, dst ...string) error {
	return l.transferContext().Save(src, dst...)
}
