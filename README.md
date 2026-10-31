<img src="logo.png" alt="Shipyard" width="400"/>

_Containerized builds and patching, driven by declarative Shipfiles._

Shipyard builds generic software and native OS packages inside Dagger
containers. You describe _what_ to build in a `shipfile.py`; Shipyard
handles fetching sources, applying patches, building, testing, and
exporting artifacts.

## Shipfiles

A build is a Python class. This one compiles a Go binary and tests it:

```python
from shipyard import Builder, Tester, Run, test, SaveIfExists

class Shipfile(Builder, Tester):
    Name = "hello"
    BaseImage = "golang:1.22-alpine"  # container the build runs in

    def build(self):
        Run("go build -o hello main.go")

    def save(self):
        SaveIfExists("hello", "hello")  # declare the artifact to export

    @test("runs", "the binary executes")
    def test_runs(self):
        Run("./hello | grep -q hello")
```

`Patcher` subclasses fetch distro sources and apply `@CodePatch` transforms instead of cloning a repo — same `save()` / `@test` shape. See [`examples/`](examples/) for runnable Shipfiles.

## Quick start

```bash
# check a shipfile is valid
shipyard validate -f shipfile.py

# show versions, metadata
shipyard versions
shipyard info

# generic build in a container (BaseImage comes from the shipfile)
shipyard build

# build a distro package (Debian family shown)
shipyard build --package debian:bookworm openssh

# run tests on extra systems after building (each gets its own section)
shipyard build --test-system ubuntu:24.04 --test-system fedora:38

# work with upstream sources without containers
shipyard pull openssh
shipyard checkout V_9_9_P2
shipyard patch /path/to/tree
shipyard export >> patchfile.diff
```

See [`docs/`](docs/) for the full reference (`Shipfile.md`, `CodePatch.md`,
`Building.md`, `LocalWorkflow.md`) and [`examples/`](examples/) for
runnable Shipfiles.

## Demo

Watch a build with multi-system tests (`demo/showcase.cast`, playable with
[asciinema](https://asciinema.org/), or re-record it with `./demo/showcase.sh`):

```
asciinema play demo/showcase.cast
```

Watch it (24s build + two-system fan-out):

<video src="https://raw.githubusercontent.com/nullmonk/shipyard/develop/demo/showcase.mp4" controls width="100%"></video>

Or play the terminal recording: `asciinema play demo/showcase.cast` (re-record with `./demo/showcase.sh`).

Re-render from the cast (`demo/render_cast.py` needs `pyte` + `pillow`):

```
python3 demo/render_cast.py demo/showcase.cast demo/frames
ffmpeg -f concat -safe 0 -i demo/frames/concat.txt -c:v libx264 -pix_fmt yuv420p -movflags +faststart demo/showcase.mp4
```

## Install

Requires Go 1.26+ and Docker (Dagger engine):

```bash
make build   # produces bin/shipyard
```
