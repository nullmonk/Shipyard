package builder

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	shipmod "shipyard/pkg/shipfile/modules/shipyard"
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
	if len(argv) == 3 && (argv[0] == "/bin/sh" || argv[0] == "/bin/bash" || argv[0] == "sh" || argv[0] == "bash") && argv[1] == "-c" {
		displayCmd = argv[2]
	} else if stdin != "" {
		for _, line := range strings.Split(stdin, "\n") {
			if s := strings.TrimSpace(line); s != "" && !strings.HasPrefix(s, "#") {
				displayCmd = s + " …"
				break
			}
		}
		if displayCmd == "" {
			for _, line := range strings.Split(stdin, "\n") {
				if s := strings.TrimSpace(line); s != "" {
					displayCmd = s + " …"
					break
				}
			}
		}
		if displayCmd == "" {
			displayCmd = "<script>"
		}
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

func (l *LocalBuilder) Copy(srcPath, dst, fromContainer string) error {
	absSrc := srcPath
	if !filepath.IsAbs(absSrc) {
		absSrc = filepath.Join(l.workdir, srcPath)
	}
	absDst := dst
	if !filepath.IsAbs(absDst) {
		absDst = filepath.Join(l.workdir, dst)
	}
	if absSrc == absDst || (dst == "." && (srcPath == "." || srcPath == "")) {
		return nil
	}

	if strings.ContainsAny(srcPath, "*?[]") {
		matches, err := filepath.Glob(absSrc)
		if err != nil {
			return err
		}
		for _, m := range matches {
			target := filepath.Join(absDst, filepath.Base(m))
			absM, _ := filepath.Abs(m)
			absTarget, _ := filepath.Abs(target)
			if absM == absTarget {
				continue
			}
			if err := copyPathLocal(m, target); err != nil {
				return err
			}
		}
		return nil
	}

	_, err := os.Stat(absSrc)
	if err != nil {
		target := absDst
		if fiDst, errDst := os.Stat(target); errDst == nil && (fiDst.IsDir() || fiDst.Mode().IsRegular()) {
			return nil
		}
		return fmt.Errorf("local copy: %w", err)
	}

	target := absDst
	if dst == "." || strings.HasSuffix(dst, "/") || strings.HasSuffix(dst, string(filepath.Separator)) {
		target = filepath.Join(absDst, filepath.Base(absSrc))
	} else if fiDst, errDst := os.Stat(absDst); errDst == nil && fiDst.IsDir() {
		target = filepath.Join(absDst, filepath.Base(absSrc))
	}

	if absSrc == target {
		return nil
	}
	return copyPathLocal(absSrc, target)
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

func (l *LocalBuilder) Save(src, dst string) error {
	l.Saves = append(l.Saves, [2]string{src, dst})
	if l.outDir == "" {
		return nil
	}
	absSrc := src
	if !filepath.IsAbs(absSrc) {
		absSrc = filepath.Join(l.workdir, src)
	}
	target := filepath.Join(l.outDir, dst)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	return copyPathLocal(absSrc, target)
}
