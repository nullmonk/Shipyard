package transfer

import (
	"fmt"
	"path"
	"strings"
)

// Standard container identifiers used across transfer operations.
const (
	ContainerContext   = "context"
	ContainerCurrent   = "current"
	ContainerArtifacts = "artifacts"
)

// Endpoint specifies a source or destination location.
// Container is the name or ID of the stage/container; empty implies host/local.
// Path is an absolute path or relative to the container CWD.
type Endpoint struct {
	Container string // Name or ID; empty implies host/local
	Path      string // Absolute path or relative to container CWD
}

// EndpointRule defines resolution constraints for an endpoint.
type EndpointRule struct {
	DefaultContainer string
	LockedContainer  string // Rejects or overrides explicit container prefix
}

// Task represents an individual planned transfer unit.
type Task struct {
	SrcContainer string
	SrcPath      string
	DstContainer string
	DstPath      string
	IsDir        bool
}

// Driver provides the underlying container filesystem operations needed to
// inspect paths, expand globs and directory contents, and execute tasks.
type Driver interface {
	// ExpandSource expands srcPath within the container.
	// If srcPath is a glob pattern or represents directory contents,
	// isGlob should be true and items contains the matched paths.
	// Otherwise isGlob should be false and items contains []string{srcPath}.
	ExpandSource(container, srcPath string) (items []string, isGlob bool, err error)

	// IsDir returns true if path in container is a directory.
	IsDir(container, path string) (bool, error)

	// IsDirTarget returns true if dstPath in container is a directory target.
	IsDirTarget(container, path string) (bool, error)

	// RunTasks executes the planned transfer tasks.
	RunTasks(tasks []Task) error
}

// Context coordinates file transfer operations between containers, context bundles,
// and artifact outputs.
//
// Note: The artifacts output is designed as a "container" with a raw layer for
// the exported files, annotated with a custom description and title.
type Context struct {
	CurrentContainer   string
	ContextContainer   string
	ArtifactsContainer string
	Driver             Driver
}

// NewContext returns a Context initialized with standard container names if omitted.
func NewContext(driver Driver, current, contextBundle, artifacts string) *Context {
	if current == "" {
		current = ContainerCurrent
	}
	if contextBundle == "" {
		contextBundle = ContainerContext
	}
	if artifacts == "" {
		artifacts = ContainerArtifacts
	}
	return &Context{
		CurrentContainer:   current,
		ContextContainer:   contextBundle,
		ArtifactsContainer: artifacts,
		Driver:             driver,
	}
}

// Copy copies from a source endpoint to a destination endpoint.
// srcRaw defaults to c.ContextContainer if no container prefix is given.
// dstRaw defaults to c.CurrentContainer if no container prefix is given,
// and defaults to "." if no destination is provided.
func (c *Context) Copy(srcRaw string, dstRaw ...string) error {
	dstTarget := "."
	if len(dstRaw) > 0 && dstRaw[0] != "" {
		dstTarget = dstRaw[0]
	}

	srcCtr := c.ContextContainer
	if srcCtr == "" {
		srcCtr = ContainerContext
	}
	dstCtr := c.CurrentContainer
	if dstCtr == "" {
		dstCtr = ContainerCurrent
	}

	src, err := ResolveEndpoint(srcRaw, EndpointRule{DefaultContainer: srcCtr})
	if err != nil {
		return err
	}

	dst, err := ResolveEndpoint(dstTarget, EndpointRule{DefaultContainer: dstCtr})
	if err != nil {
		return err
	}

	return c.PlanAndExecute(src, dst)
}

// Save records a file, directory, or glob for export to the artifacts container.
// The destination container is locked to c.ArtifactsContainer; container overrides
// are rejected.
//
// The artifacts output is a container with a raw layer for files and custom metadata
// (description and title).
func (c *Context) Save(srcRaw string, dstRaw ...string) error {
	dstTarget := "."
	if len(dstRaw) > 0 && dstRaw[0] != "" {
		dstTarget = dstRaw[0]
	}

	srcCtr := c.CurrentContainer
	if srcCtr == "" {
		srcCtr = ContainerCurrent
	}
	artCtr := c.ArtifactsContainer
	if artCtr == "" {
		artCtr = ContainerArtifacts
	}

	src, err := ResolveEndpoint(srcRaw, EndpointRule{DefaultContainer: srcCtr})
	if err != nil {
		return err
	}

	dst, err := ResolveEndpoint(dstTarget, EndpointRule{LockedContainer: artCtr})
	if err != nil {
		return err
	}

	return c.PlanAndExecute(src, dst)
}

func splitContainerPath(raw string) (string, string) {
	if i := strings.IndexByte(raw, ':'); i > 0 {
		// Do not treat Windows drive letters (e.g. C:\) as container prefixes.
		if i == 1 && len(raw) > 2 && (raw[2] == '\\' || raw[2] == '/') &&
			((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) {
			return "", raw
		}
		return raw[:i], raw[i+1:]
	}
	return "", raw
}

// ResolveEndpoint parses an endpoint string and applies the given rule.
func ResolveEndpoint(raw string, rule EndpointRule) (Endpoint, error) {
	ctr, p := splitContainerPath(raw)

	if rule.LockedContainer != "" {
		if ctr != "" && ctr != rule.LockedContainer {
			return Endpoint{}, fmt.Errorf("target container locked: container override not permitted")
		}
		ctr = rule.LockedContainer
	} else if ctr == "" {
		ctr = rule.DefaultContainer
	}

	return Endpoint{Container: ctr, Path: p}, nil
}

// Plan computes the list of transfer Tasks required to satisfy copying from src to dst.
func (c *Context) Plan(src, dst Endpoint) ([]Task, error) {
	if c.Driver == nil {
		return nil, fmt.Errorf("transfer context has no driver configured")
	}

	srcPattern := src.Path
	treatAsGlob := false
	if src.Path == "." || src.Path == "./" {
		srcPattern = "*"
		treatAsGlob = true
	} else if strings.HasSuffix(src.Path, "/.") {
		trimmed := strings.TrimSuffix(src.Path, "/.")
		if trimmed == "" || trimmed == "." {
			srcPattern = "*"
		} else {
			srcPattern = trimmed + "/*"
		}
		treatAsGlob = true
	}

	items, isGlob, err := c.Driver.ExpandSource(src.Container, srcPattern)
	if err != nil {
		return nil, err
	}
	if treatAsGlob {
		isGlob = true
	}

	var tasks []Task
	for _, item := range items {
		isDir, err := c.Driver.IsDir(src.Container, item)
		if err != nil {
			return nil, err
		}

		isTargetDir, err := c.Driver.IsDirTarget(dst.Container, dst.Path)
		if err != nil {
			return nil, err
		}

		finalDst := dst.Path
		baseItem := path.Base(path.Clean(item))

		if isGlob {
			// Globs always deposit items directly into destination directory
			finalDst = path.Join(dst.Path, path.Base(item))
		} else if isDir {
			// Directory copies preserve folder name inside target directory
			if path.Base(path.Clean(dst.Path)) == baseItem && !strings.HasSuffix(dst.Path, "/") {
				finalDst = dst.Path
			} else {
				finalDst = path.Join(dst.Path, baseItem)
			}
		} else if isTargetDir {
			// Single file copying into directory
			finalDst = path.Join(dst.Path, path.Base(item))
		}

		tasks = append(tasks, Task{
			SrcContainer: src.Container,
			SrcPath:      item,
			DstContainer: dst.Container,
			DstPath:      finalDst,
			IsDir:        isDir,
		})
	}

	return tasks, nil
}

// PlanAndExecute plans the transfer tasks and runs them using the driver.
func (c *Context) PlanAndExecute(src, dst Endpoint) error {
	tasks, err := c.Plan(src, dst)
	if err != nil {
		return err
	}
	return c.Driver.RunTasks(tasks)
}

