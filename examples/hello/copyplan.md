# Container File Transfer Specification

## Path Resolution Matrix

| Call | Source Evaluated | Destination Evaluated | Destination Layout |
| --- | --- | --- | --- |
| `Copy("pkg/*")` | `context:pkg/a`<br>

<br>`context:pkg/b` | `current:.` | `./a`<br>

<br>`./b` |
| `Copy("pkg/")` | `context:pkg` (directory) | `current:.` | `./pkg/a`<br>

<br>`./pkg/b` |
| `Copy("pkg")` | `context:pkg` (directory) | `current:.` | `./pkg/a`<br>

<br>`./pkg/b` |
| `Copy("pkg/.", "out/")` | `context:pkg/*` (contents) | `current:out/` | `./out/a`<br>

<br>`./out/b` |
| `Copy("main.go")` | `context:main.go` (file) | `current:.` | `./main.go` |
| `Copy("main.go", "app.go")` | `context:main.go` (file) | `current:app.go` | `./app.go` |
| `Copy("foo:src/*", "bar:dst/")` | `foo:src/*` | `bar:dst/` | `bar:dst/*` |
| `Save("build/*")` | `current:build/*` | `artifacts:.` | `./*` |
| `Save("bin", "release/")` | `current:bin` (directory) | `artifacts:release/` | `artifacts:release/bin/` |
| `Save("bin", "other:rel/")` | — | `other:rel/` | **Error:** Target container locked |

---

## Core Structs

```go
package transfer

import "path"

type Endpoint struct {
	Container string // Name or ID; empty implies host/local
	Path      string // Absolute path or relative to container CWD
}

type EndpointRule struct {
	DefaultContainer string
	LockedContainer  string // Rejects or overrides explicit container prefix
}

type Task struct {
	SrcContainer string
	SrcPath      string
	DstContainer string
	DstPath      string
	IsDir        bool
}

type Context struct {
	CurrentContainer   string
	ContextContainer   string
	ArtifactsContainer string
}

```

---

## Artifacts Container Architecture

The artifacts output is designed and treated as a "container" (`ArtifactsContainer`, defaulting to `"artifacts"`):
- The artifacts output will be a **container** with a raw layer for the files.
- It is annotated with a **custom description** and **custom title**.
- In `Save(src, dst)`, the destination container is locked to the artifacts container (`LockedContainer`), rejecting explicit container overrides (e.g. `Save("bin", "other:rel/")` returns an error).


---

## Pseudo-Code Implementation

```go
func (c *Context) Copy(srcRaw string, dstRaw ...string) error {
	dstTarget := "."
	if len(dstRaw) > 0 && dstRaw[0] != "" {
		dstTarget = dstRaw[0]
	}

	src, err := resolveEndpoint(srcRaw, EndpointRule{DefaultContainer: c.ContextContainer})
	if err != nil {
		return err
	}

	dst, err := resolveEndpoint(dstTarget, EndpointRule{DefaultContainer: c.CurrentContainer})
	if err != nil {
		return err
	}

	return planAndExecute(src, dst)
}

func (c *Context) Save(srcRaw string, dstRaw ...string) error {
	dstTarget := "."
	if len(dstRaw) > 0 && dstRaw[0] != "" {
		dstTarget = dstRaw[0]
	}

	src, err := resolveEndpoint(srcRaw, EndpointRule{DefaultContainer: c.CurrentContainer})
	if err != nil {
		return err
	}

	dst, err := resolveEndpoint(dstTarget, EndpointRule{LockedContainer: c.ArtifactsContainer})
	if err != nil {
		return err
	}

	return planAndExecute(src, dst)
}

func resolveEndpoint(raw string, rule EndpointRule) (Endpoint, error) {
	var ctr, p string
	if parts := splitContainerPath(raw); len(parts) == 2 {
		ctr, p = parts[0], parts[1]
	} else {
		p = raw
	}

	if rule.LockedContainer != "" {
		if ctr != "" && ctr != rule.LockedContainer {
			return Endpoint{}, error("container override not permitted")
		}
		ctr = rule.LockedContainer
	} else if ctr == "" {
		ctr = rule.DefaultContainer
	}

	return Endpoint{Container: ctr, Path: p}, nil
}

func planAndExecute(src, dst Endpoint) error {
	// 1. Expand Globs: "pkg/*" -> ["pkg/a", "pkg/b"]
	items, isGlob := expandSource(src.Container, src.Path)

	var tasks []Task
	for _, item := range items {
		isDir := inspectIsDir(src.Container, item)

		finalDst := dst.Path
		if isGlob {
			// Globs always deposit items directly into destination directory
			finalDst = path.Join(dst.Path, path.Base(item))
		} else if isDir {
			// Directory copies preserve folder name inside target directory
			finalDst = path.Join(dst.Path, path.Base(path.Clean(item)))
		} else if isDirTarget(dst.Container, dst.Path) {
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

	return runTasks(tasks)
}

```