package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"dagger.io/dagger"
	shipmod "shipyard/pkg/shipfile/modules/shipyard"
)

// DaggerBuilder implements shipmod.ContainerBuilder by routing each Python
// From/Use/Run/Copy/Write/Script/Env/User/Workdir call to the Dagger API.
// Multi-stage builds are supported: From(image, "name") names a stage,
// Use("name") switches the active stage, and Copy("stage:path", dst) copies
// across stages.
type DaggerBuilder struct {
	ctx    context.Context
	client *dagger.Client
	sfDir  string
	source *dagger.Directory // ContextBundle: upstream source for Copy("path", dst)

	out    io.Writer
	cmdOut io.Writer
	cmdErr io.Writer

	verbose bool

	// stages holds all named containers; activeName is the currently active stage.
	stages     map[string]*dagger.Container
	activeName string
	fromCount  int

	// Container always mirrors the active container.
	// The caller reads this after RunWithBuilder returns to get the final image.
	Container *dagger.Container

	// Saves accumulates Save() calls for deferred export by BuildShipfile.
	// If non-empty, the artifact regex scan is skipped.
	Saves []SaveOp
}

// SaveOp records a single Save(src, dst) call for deferred export.
type SaveOp struct {
	Container *dagger.Container // snapshot at the time Save() was called
	SrcPath   string
	DstPath   string
}

var _ shipmod.ContainerBuilder = (*DaggerBuilder)(nil)

func (d *DaggerBuilder) active() *dagger.Container {
	return d.Container
}

func (d *DaggerBuilder) setActive(ctr *dagger.Container) {
	d.Container = ctr
	if d.activeName != "" {
		d.stages[d.activeName] = ctr
	}
}

// From sets the base image, optionally naming the stage.
// A second call without a name is an error — all stages must be named.
func (d *DaggerBuilder) From(image, name string) error {
	if d.fromCount > 0 && name == "" {
		return fmt.Errorf("multiple From() calls require a name — e.g. From(%q, \"stage\")", image)
	}
	fmt.Fprintf(d.out, "[*] FROM %s", image)
	if name != "" {
		fmt.Fprintf(d.out, " AS %s", name)
	}
	fmt.Fprintln(d.out)

	ctr := d.client.Container().From(image)
	if d.stages == nil {
		d.stages = make(map[string]*dagger.Container)
	}
	if name != "" {
		d.stages[name] = ctr
	}
	d.activeName = name
	d.Container = ctr
	d.fromCount++
	return nil
}

// Use switches the active container to a previously named stage.
func (d *DaggerBuilder) Use(name string) error {
	if d.stages == nil {
		return fmt.Errorf("Use(%q): no named stages exist yet", name)
	}
	ctr, ok := d.stages[name]
	if !ok {
		names := make([]string, 0, len(d.stages))
		for k := range d.stages {
			names = append(names, k)
		}
		return fmt.Errorf("Use(%q): unknown stage; known stages: %s", name, strings.Join(names, ", "))
	}
	d.activeName = name
	d.Container = ctr
	return nil
}

// Run executes a command in the active container, syncing each layer immediately
// so stdout is available and the layer is independently cached.
func (d *DaggerBuilder) Run(argv []string, stdin string) (stdout, stderr string, rc int, err error) {
	if d.active() == nil {
		return "", "", -1, fmt.Errorf("Run() called before From()")
	}

	var displayCmd string
	if len(argv) == 3 && argv[0] == "/bin/sh" && argv[1] == "-c" {
		displayCmd = argv[2]
	} else if len(argv) == 1 && argv[0] == "/bin/sh" && stdin != "" {
		// Script() path: show first non-empty line as preview
		for _, line := range strings.Split(stdin, "\n") {
			if s := strings.TrimSpace(line); s != "" && !strings.HasPrefix(s, "#") {
				displayCmd = s + " …"
				break
			}
		}
		if displayCmd == "" {
			displayCmd = "<script>"
		}
	} else {
		displayCmd = strings.Join(argv, " ")
	}
	fmt.Fprintf(d.out, "    $ %s... ", truncate(displayCmd, 72))
	t0 := time.Now()

	opts := dagger.ContainerWithExecOpts{}
	if stdin != "" {
		opts.Stdin = stdin
	}

	next := d.active().WithExec(argv, opts)
	synced, syncErr := next.Sync(d.ctx)
	elapsed := time.Since(t0)

	if syncErr != nil {
		fmt.Fprintln(d.out, "FAILED")
		var execErr *dagger.ExecError
		if errors.As(syncErr, &execErr) {
			if execErr.Stdout != "" {
				fmt.Fprint(d.cmdOut, execErr.Stdout)
			}
			if execErr.Stderr != "" {
				fmt.Fprint(d.cmdErr, execErr.Stderr)
			}
			return execErr.Stdout, execErr.Stderr, execErr.ExitCode, nil
		}
		return "", "", -1, syncErr
	}

	fmt.Fprintf(d.out, "done (%.1fs)\n", elapsed.Seconds())
	stdout, _ = synced.Stdout(d.ctx)
	stderr, _ = synced.Stderr(d.ctx)
	d.setActive(synced)
	if stdout != "" {
		fmt.Fprint(d.cmdOut, stdout)
	}
	if stderr != "" {
		fmt.Fprint(d.cmdErr, stderr)
	}
	return stdout, stderr, 0, nil
}

// Copy copies into the active container.
//
//   - fromContainer == "": copy srcPath from the ContextBundle (upstream source).
//     srcPath "." copies the entire bundle.
//   - fromContainer != "": copy srcPath from that named stage.
//     srcPath "." copies the entire stage root.
func (d *DaggerBuilder) Copy(srcPath, dst, fromContainer string) error {
	if d.active() == nil {
		return fmt.Errorf("Copy() called before From()")
	}
	if d.verbose {
		if fromContainer != "" {
			fmt.Fprintf(d.out, "    COPY %s:%s → %s\n", fromContainer, srcPath, dst)
		} else {
			fmt.Fprintf(d.out, "    COPY %s → %s\n", srcPath, dst)
		}
	}

	var srcDir *dagger.Directory
	if fromContainer != "" {
		// Copy from a named stage.
		if d.stages == nil {
			return fmt.Errorf("Copy(%q:%q): no named stages exist", fromContainer, srcPath)
		}
		src, ok := d.stages[fromContainer]
		if !ok {
			return fmt.Errorf("Copy(): unknown stage %q", fromContainer)
		}
		srcDir = src.Rootfs()
	} else {
		// Copy from the ContextBundle (upstream source directory).
		if d.source == nil {
			return fmt.Errorf("Copy(%q): no ContextBundle available (no Url and no local sources/ directory)", srcPath)
		}
		srcDir = d.source
	}

	cleanSrc := strings.TrimPrefix(path.Clean(srcPath), "/")
	if cleanSrc == "." || cleanSrc == "" {
		d.setActive(d.active().WithDirectory(dst, srcDir))
		return nil
	}

	isGlob := strings.ContainsAny(srcPath, "*?[]")
	var matches []string
	if isGlob {
		var err error
		matches, err = srcDir.Glob(d.ctx, strings.TrimPrefix(srcPath, "/"))
		if err != nil {
			return fmt.Errorf("Copy(%q): glob: %w", srcPath, err)
		}
		if len(matches) == 0 {
			return fmt.Errorf("Copy(%q): no files matched pattern", srcPath)
		}
	} else {
		matches = []string{cleanSrc}
	}

	isDstDir := dst == "." || dst == "" || strings.HasSuffix(dst, "/")
	if !isDstDir {
		cleanDst := strings.TrimPrefix(path.Clean(dst), "/")
		dft, err := d.active().Rootfs().Stat(cleanDst).FileType(d.ctx)
		if err == nil && dft == dagger.FileTypeDirectory {
			isDstDir = true
		}
	}

	for _, m := range matches {
		m = strings.TrimPrefix(strings.TrimSuffix(m, "/"), "/")
		stat := srcDir.Stat(m)
		ft, err := stat.FileType(d.ctx)
		if err != nil {
			return fmt.Errorf("Copy(%q): stat %q: %w", srcPath, m, err)
		}

		baseName := path.Base(m)
		var target string
		if ft == dagger.FileTypeDirectory {
			if isDstDir {
				if path.Clean(dst) == baseName {
					target = dst
				} else {
					target = path.Join(dst, baseName)
				}
			} else {
				target = dst
			}
			d.setActive(d.active().WithDirectory(target, srcDir.Directory(m)))
		} else {
			if isDstDir {
				target = path.Join(dst, baseName)
			} else {
				target = dst
			}
			d.setActive(d.active().WithFile(target, srcDir.File(m)))
		}
	}

	return nil
}

// WriteFile writes a file with the given content into the active container.
func (d *DaggerBuilder) WriteFile(path, content string) error {
	if d.active() == nil {
		return fmt.Errorf("Write() called before From()")
	}
	if d.verbose {
		fmt.Fprintf(d.out, "    WRITE %s\n", path)
	}
	d.setActive(d.active().WithNewFile(path, content))
	return nil
}

// Workdir sets the working directory in the active container.
func (d *DaggerBuilder) Workdir(path string) error {
	if d.active() == nil {
		return fmt.Errorf("Workdir() called before From()")
	}
	d.setActive(d.active().WithWorkdir(path))
	return nil
}

// Env sets an environment variable in the active container.
func (d *DaggerBuilder) Env(key, value string) error {
	if d.active() == nil {
		return fmt.Errorf("Env() called before From()")
	}
	d.setActive(d.active().WithEnvVariable(key, value))
	return nil
}

// Dockerfile builds from an inline Dockerfile using the ContextBundle as the
// build context. The result becomes the active container. Stage tracking is
// reset — subsequent From/Use/Copy calls operate on the final image only.
func (d *DaggerBuilder) Dockerfile(content string) error {
	if d.source == nil {
		return fmt.Errorf("Dockerfile() requires a ContextBundle (no Url and no local sources/ directory)")
	}
	fmt.Fprintf(d.out, "[*] Building from Dockerfile... ")
	t0 := time.Now()

	// Inject the inline Dockerfile into the ContextBundle and build.
	ctx := d.source.WithNewFile("Dockerfile", content)
	synced, err := ctx.DockerBuild().Sync(d.ctx)
	elapsed := time.Since(t0)
	if err != nil {
		fmt.Fprintln(d.out, "FAILED")
		var execErr *dagger.ExecError
		if errors.As(err, &execErr) {
			fmt.Fprint(d.cmdErr, execErr.Stderr)
		}
		return err
	}
	fmt.Fprintf(d.out, "done (%.1fs)\n", elapsed.Seconds())

	// Dockerfile takes over: clear stage tracking, set result as active container.
	d.stages = nil
	d.activeName = ""
	d.fromCount = 1
	d.Container = synced
	return nil
}

// User sets the user for subsequent RUN instructions.
func (d *DaggerBuilder) User(name string) error {
	if d.active() == nil {
		return fmt.Errorf("User() called before From()")
	}
	d.setActive(d.active().WithUser(name))
	return nil
}

// Save records a file or directory for export to the artifacts output directory.
// The container is snapshotted at call time, so later layer additions don't affect it.
func (d *DaggerBuilder) Save(src, dst string) error {
	var ctr *dagger.Container
	var srcPath string

	if i := strings.IndexByte(src, ':'); i > 0 {
		name := src[:i]
		srcPath = src[i+1:]
		if d.stages == nil {
			return fmt.Errorf("Save(%q): no named stages exist", src)
		}
		var ok bool
		ctr, ok = d.stages[name]
		if !ok {
			return fmt.Errorf("Save(%q): unknown stage %q", src, name)
		}
	} else {
		ctr = d.Container
		srcPath = src
	}

	if ctr == nil {
		return fmt.Errorf("Save(%q): no container available (call From() first)", src)
	}
	d.Saves = append(d.Saves, SaveOp{Container: ctr, SrcPath: srcPath, DstPath: dst})
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
