# Shipfile
A shipfile is the workhorse of Shipyard. It controls how patches get applied, defines [CodePatches](./CodePatch.md), and defines metadata about the source. To generate a new
shipfile for a source, run the following. The URL will be the path to the source repo
```bash
# Init the file in this folder
shipyard init "<source-git-repo>"
# Init the file in another folder
shipyard -d MyProject init "<source-url>"
```

## Shipfile Options

#### Url
The URL of the git repo of the source

#### Urls
Occasionally, source code is not stored properly in a Git repo, instead,
several version URLs may be specified. From the URLs, a Git repo will be created and tagged
properly from the URLs

#### VersionTags
A [glob(7)](https://www.man7.org/linux/man-pages/man7/glob.7.html) patch that will match
git tags which denote versions. The pattern can be tested in the source repo by using the command `git tag -l 'PATTERN'`.

> Currently, the only way for Shipyard to denote versions is with git tags. This may update later if the use case comes up

#### Patches
The directory to search in for patch files. This directory will contain version folders which then contain several patch files.

#### Package
The default name of the package to be built. If specified here, the `PACKAGE` argument in the `shipyard build` command becomes optional.

```python
class Shipfile:
    ...
    Package = "proftpd"
```

If you run `shipyard build debian:bookworm`, it will use "proftpd". You can override this by explicitly providing the package name: `shipyard build debian:bookworm other-package`.

#### source_directory
The directory which contains the source repo, relative to the Shipfile. Can either be a string or a function which returns a string.

#### Variables
Sometimes its useful to quickly change strings in a binary or to allow a builder to update values without touching the patchfiles. This is possible with the `Variables` dictionary in the Shipfile

```python
import uuid

class Shipfile:
    ...

    Variables = {
        "__PASSWORD__": "bananananas", # Special Password
        "__BUILD_UUID__": uuid.uuid4(), # Random UUID to put in the code
    }
```

Variables are only subsituted when running `shipyard export`.

### Functions

#### `tag_to_version(tag) -> str`

#### `version_to_tag(version) -> str`

#### `is_version_ignored(version: shipyard.Version) -> bool`

## Generic Builders

Not every project is an OS package. A Shipfile becomes a **generic builder** by defining
`Build`: instead of fetching source through a distro's package manager and building with
`dpkg`/`rpmbuild`/`makepkg`, Shipyard clones `Url` itself, runs your CodePatches against it,
drops it into a container you choose, and runs your own scripts. This is a good fit for
anything that builds with its own toolchain (Go, Rust, Node, a plain Makefile, ...).

```python
class Shipfile:
    Name = "myapp"
    Url = "https://github.com/me/myapp"

    BaseImage = "golang:1.22"
    Setup = "apt-get update && apt-get install -y upx"   # optional, runs before Build
    Build = "go build -o myapp ./cmd/myapp"
    Artifacts = r"^myapp$"                                # regex matched against ArtifactDir
```

Run it with `shipyard build` (the image argument is optional since `BaseImage` is set):

```bash
shipyard build
# or, to override the image:
shipyard build golang:1.23
```

#### BaseImage
The container image the project is built in, e.g. `"golang:1.22"`. Required unless `image` is
passed explicitly to `shipyard build`.

#### Setup
An optional shell script (passed to `/bin/sh -c`) that prepares the build environment, e.g.
installing extra system packages. Runs once, before `Build`.

#### Build
The shell script (passed to `/bin/sh -c`) that actually builds the project, with the patched
source as its working directory. Defining `Build` is what marks a Shipfile as a generic builder.

#### Artifacts
A regex matched against the paths found in `ArtifactDir` (or `--artifacts` on the CLI, which
takes precedence) to decide which build outputs get exported to the host.

#### ArtifactDir
Where to look for `Artifacts`. Defaults to the source directory (where `Build` ran).