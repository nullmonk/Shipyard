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
	"shipyard/pkg/transfer"
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

	// hasImage tracks whether a base image has been selected, either by
	// seeding the builder from --image/BaseImage or by calling From() (any
	// form, including named stages) or Dockerfile(). The generic builder
	// starts from an empty client.Container() when no image is configured so
	// build() may call From() later; without this flag Run() would proceed
	// against nothing and surface a confusing downstream error (observed as
	// fork/exec /bin/sh: no such file or directory from a setup() Run with
	// no image anywhere). Guarded in Run() via requireImage before executing.
	hasImage bool

	// Container always mirrors the active container.
	// The caller reads this after RunWithBuilder returns to get the final image.
	Container *dagger.Container

	// Saves accumulates Save() calls for deferred export by BuildShipfile.
	// If non-empty, the artifact regex scan is skipped.
	Saves []SaveOp

	workdir       string
	stageWorkdirs map[string]string
}

// SaveOp records a single Save(src, dst) call for deferred export.
// Note: The artifacts output is a "container" with a raw layer for the files
// and a custom description and title.
type SaveOp struct {
	Container *dagger.Container // snapshot at the time Save() was called
	SrcPath   string
	DstPath   string
	IsDir     bool
}

var _ shipmod.ContainerBuilder = (*DaggerBuilder)(nil)

// IsContainerBuild reports that DaggerBuilder drives build() host-side while
// targeting a container. pkg/shipfile probes this via
// shipmod.IsContainerDriven to seed SHIPYARD_IN_CONTAINER=1 into the gpython
// sandbox environ for setup()/build(). LocalBuilder deliberately does NOT
// implement it, so --local runs never observe the marker.
func (d *DaggerBuilder) IsContainerBuild() bool { return true }

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
// requireImage returns a clear fail-fast error when no base image has been
// selected yet. Callers must check this before executing against the
// container so an imageless Run() fails here instead of against an empty
// client.Container().
func (d *DaggerBuilder) requireImage(op string) error {
	if !d.hasImage {
		return fmt.Errorf("no base image selected: set BaseImage, pass --image, or call From() before %s()", op)
	}
	return nil
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

	// A nil client only happens in engine-free unit tests; still record the
	// image selection so the hasImage guard can be exercised without Dagger.
	var ctr *dagger.Container
	if d.client != nil {
		ctr = d.client.Container().From(image)
	}
	if d.stages == nil {
		d.stages = make(map[string]*dagger.Container)
	}
	if name != "" {
		d.stages[name] = ctr
	}
	d.activeName = name
	d.Container = ctr
	d.fromCount++
	d.hasImage = true
	d.workdir = ""
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
	if d.stageWorkdirs != nil {
		d.workdir = d.stageWorkdirs[name]
	} else {
		d.workdir = ""
	}
	return nil
}

// Run executes a command in the active container, syncing each layer immediately
// so stdout is available and the layer is independently cached.
func (d *DaggerBuilder) Run(argv []string, stdin string) (stdout, stderr string, rc int, err error) {
	// Fail fast on imageless exec. Exec path diagnosis: shipfile Run()/Script()
	// (pkg/shipfile/modules/shipyard/run.go) routes via GetActiveBuilder()
	// when a Builder drives the build, so a setup()/build() Run lands here in
	// DaggerBuilder.Run — NOT in executeHostCommand (host/local fallback, used
	// only when no builder is active). The empty client.Container() seed is
	// non-nil, so the nil-container check below cannot catch it; the hasImage
	// flag is the correct guard layer. LocalBuilder/host semantics untouched.
	if err := d.requireImage("Run"); err != nil {
		return "", "", -1, err
	}
	if d.active() == nil {
		return "", "", -1, fmt.Errorf("Run() called before From()")
	}

	var displayCmd string
	if len(argv) == 3 && shipmod.IsShell(argv[0]) && argv[1] == "-c" {
		displayCmd = argv[2]
	} else if stdin != "" && len(argv) > 0 && shipmod.IsShell(argv[0]) {
		displayCmd = shipmod.FormatScriptDisplayCmd(stdin)
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

func (d *DaggerBuilder) activeContainerName() string {
	if d.activeName != "" {
		return d.activeName
	}
	return transfer.ContainerCurrent
}

func (d *DaggerBuilder) transferContext() *transfer.Context {
	return transfer.NewContext(
		&daggerTransferDriver{d: d},
		d.activeContainerName(),
		transfer.ContainerContext,
		transfer.ContainerArtifacts,
	)
}

// Copy copies into the active container using the transfer specification.
//
// Supports:
//   - Copy(src)                       -> copies from ContextBundle into active container "."
//   - Copy(src, dst)                  -> copies from ContextBundle into active container dst
//   - Copy("stage:src", dst)          -> copies from named stage into active container dst
//   - Copy("src", dst, fromContainer) -> backwards-compatible signature
func (d *DaggerBuilder) Copy(srcPath string, dstAndFrom ...string) error {
	if d.active() == nil {
		return fmt.Errorf("Copy() called before From()")
	}
	src := srcPath
	var dstArgs []string
	if len(dstAndFrom) >= 2 && dstAndFrom[1] != "" && !strings.Contains(src, ":") {
		src = dstAndFrom[1] + ":" + src
		dstArgs = []string{dstAndFrom[0]}
	} else if len(dstAndFrom) >= 1 {
		dstArgs = []string{dstAndFrom[0]}
	}
	return d.transferContext().Copy(src, dstArgs...)
}

type daggerTransferDriver struct {
	d *DaggerBuilder
}

func (drv *daggerTransferDriver) resolveContainer(container string) (*dagger.Directory, string, *dagger.Container, error) {
	d := drv.d
	switch container {
	case transfer.ContainerContext, "":
		if d.source == nil {
			return nil, "", nil, fmt.Errorf("no ContextBundle available (no Url and no local sources/ directory)")
		}
		return d.source, "", nil, nil
	case transfer.ContainerCurrent, d.activeName:
		if d.active() == nil {
			return nil, "", nil, fmt.Errorf("no container available (call From() first)")
		}
		return d.active().Rootfs(), d.workdir, d.active(), nil
	case transfer.ContainerArtifacts:
		return nil, "", nil, nil
	default:
		if d.stages == nil {
			return nil, "", nil, fmt.Errorf("unknown container %q: no named stages exist", container)
		}
		c, ok := d.stages[container]
		if !ok {
			return nil, "", nil, fmt.Errorf("unknown stage %q", container)
		}
		wd := ""
		if d.stageWorkdirs != nil {
			wd = d.stageWorkdirs[container]
		}
		return c.Rootfs(), wd, c, nil
	}
}

func (drv *daggerTransferDriver) ExpandSource(container, srcPath string) ([]string, bool, error) {
	d := drv.d
	dir, workdir, _, err := drv.resolveContainer(container)
	if err != nil {
		return nil, false, err
	}

	effectiveSrc := srcPath
	if !path.IsAbs(effectiveSrc) && workdir != "" {
		effectiveSrc = path.Join(workdir, effectiveSrc)
	}

	isGlob := strings.ContainsAny(srcPath, "*?[]")
	if isGlob {
		cleanPattern := strings.TrimPrefix(path.Clean(effectiveSrc), "/")
		matches, err := dir.Glob(d.ctx, cleanPattern)
		if err != nil {
			return nil, true, fmt.Errorf("glob: %w", err)
		}
		if len(matches) == 0 {
			return nil, true, fmt.Errorf("no files matched pattern %q", srcPath)
		}
		var result []string
		for _, m := range matches {
			result = append(result, "/"+strings.TrimPrefix(m, "/"))
		}
		return result, true, nil
	}

	cleanPath := strings.TrimPrefix(path.Clean(effectiveSrc), "/")
	if cleanPath == "" || cleanPath == "." {
		return []string{"/"}, false, nil
	}
	stat := dir.Stat(cleanPath)
	if _, err := stat.FileType(d.ctx); err != nil {
		return nil, false, fmt.Errorf("stat %q: %w", srcPath, err)
	}
	return []string{"/" + cleanPath}, false, nil
}

func (drv *daggerTransferDriver) IsDir(container, itemPath string) (bool, error) {
	d := drv.d
	dir, _, _, err := drv.resolveContainer(container)
	if err != nil {
		return false, err
	}
	cleanPath := strings.TrimPrefix(path.Clean(itemPath), "/")
	if cleanPath == "" || cleanPath == "." {
		return true, nil
	}
	ft, err := dir.Stat(cleanPath).FileType(d.ctx)
	if err != nil {
		return false, err
	}
	return ft == dagger.FileTypeDirectory, nil
}

func (drv *daggerTransferDriver) IsDirTarget(container, dstPath string) (bool, error) {
	if container == transfer.ContainerArtifacts {
		return strings.HasSuffix(dstPath, "/") || dstPath == "." || dstPath == "", nil
	}
	if strings.HasSuffix(dstPath, "/") || dstPath == "." || dstPath == "" {
		return true, nil
	}
	dir, workdir, _, err := drv.resolveContainer(container)
	if err != nil {
		return false, nil
	}
	effectiveDst := dstPath
	if !path.IsAbs(effectiveDst) && workdir != "" {
		effectiveDst = path.Join(workdir, effectiveDst)
	}
	cleanDst := strings.TrimPrefix(path.Clean(effectiveDst), "/")
	if cleanDst == "" || cleanDst == "." {
		return true, nil
	}
	ft, err := dir.Stat(cleanDst).FileType(drv.d.ctx)
	if err == nil && ft == dagger.FileTypeDirectory {
		return true, nil
	}
	return false, nil
}

func (drv *daggerTransferDriver) RunTasks(tasks []transfer.Task) error {
	d := drv.d
	for _, task := range tasks {
		if task.DstContainer == transfer.ContainerArtifacts {
			// Save operation: snapshot container and record SaveOp
			_, _, ctr, err := drv.resolveContainer(task.SrcContainer)
			if err != nil {
				return err
			}
			if ctr == nil {
				return fmt.Errorf("no container available for save (call From() first)")
			}
			if d.verbose {
				fmt.Fprintf(d.out, "    SAVE %s → %s\n", task.SrcPath, task.DstPath)
			}
			d.Saves = append(d.Saves, SaveOp{
				Container: ctr,
				SrcPath:   task.SrcPath,
				DstPath:   task.DstPath,
				IsDir:     task.IsDir,
			})
			continue
		}

		// Copy operation
		srcDir, _, _, err := drv.resolveContainer(task.SrcContainer)
		if err != nil {
			return err
		}

		cleanSrc := strings.TrimPrefix(path.Clean(task.SrcPath), "/")

		dstWorkdir := d.workdir
		if task.DstContainer != "" && task.DstContainer != transfer.ContainerCurrent && task.DstContainer != d.activeName {
			if d.stageWorkdirs != nil {
				dstWorkdir = d.stageWorkdirs[task.DstContainer]
			}
		}

		effectiveDst := task.DstPath
		if effectiveDst == "" || effectiveDst == "." {
			if dstWorkdir != "" {
				effectiveDst = dstWorkdir
			} else {
				effectiveDst = "/"
			}
		} else if !path.IsAbs(effectiveDst) {
			if dstWorkdir != "" {
				effectiveDst = path.Join(dstWorkdir, effectiveDst)
			} else {
				effectiveDst = "/" + effectiveDst
			}
		}

		if d.verbose {
			if task.SrcContainer != "" && task.SrcContainer != transfer.ContainerContext {
				fmt.Fprintf(d.out, "    COPY %s:%s → %s\n", task.SrcContainer, task.SrcPath, effectiveDst)
			} else {
				fmt.Fprintf(d.out, "    COPY %s → %s\n", task.SrcPath, effectiveDst)
			}
		}

		isDstActive := (task.DstContainer == "" || task.DstContainer == transfer.ContainerCurrent || task.DstContainer == d.activeName)
		var targetCtr *dagger.Container
		if isDstActive {
			targetCtr = d.active()
		} else {
			targetCtr = d.stages[task.DstContainer]
		}
		if targetCtr == nil {
			return fmt.Errorf("target container %q not found", task.DstContainer)
		}

		if task.IsDir {
			if cleanSrc == "" || cleanSrc == "." {
				targetCtr = targetCtr.WithDirectory(effectiveDst, srcDir)
			} else {
				targetCtr = targetCtr.WithDirectory(effectiveDst, srcDir.Directory(cleanSrc))
			}
		} else {
			targetCtr = targetCtr.WithFile(effectiveDst, srcDir.File(cleanSrc))
		}

		if isDstActive {
			d.setActive(targetCtr)
		} else {
			d.stages[task.DstContainer] = targetCtr
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
	d.workdir = path
	if d.activeName != "" {
		if d.stageWorkdirs == nil {
			d.stageWorkdirs = make(map[string]string)
		}
		d.stageWorkdirs[d.activeName] = path
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
	d.hasImage = true
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

// Save records a file, directory, or glob pattern for export to the artifacts container.
// The container is snapshotted at call time, so later layer additions don't affect it.
// The artifacts output is a container with a raw layer for the files and a custom description and title.
func (d *DaggerBuilder) Save(src string, dst ...string) error {
	if d.active() == nil && len(d.stages) == 0 {
		return fmt.Errorf("Save(%q): no container available (call From() first)", src)
	}
	return d.transferContext().Save(src, dst...)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
