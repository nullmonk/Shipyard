# Building Patches

Shipyard uses [Dagger](https://dagger.io) to build packages in isolated containers. This ensures reproducible builds across different environments.

## Build Command

The primary way to build a package is using the `shipyard build` command.

```bash
shipyard build IMAGE PACKAGE [flags]
```

### Arguments

1. `IMAGE` - The base docker image to build on. Supported distributions include:
    - `debian:*` (e.g., `debian:bookworm`)
    - `ubuntu:*`
    - `rockylinux:*` (e.g., `rockylinux:9`)
    - `centos:*` (e.g., `centos:stream9`)
    - `fedora:*`
    - `amazonlinux:*`
    - `archlinux:*` (e.g. `archlinux:base-devel`)

2. `PACKAGE` - The name of the source package to build and patch (e.g., `proftpd`, `openssh`).

### Flags

- `--version=VERSION`: (Optional) The version of the source code to use. Defaults to the current/latest version defined in the shipyard project.
- `--interactive` (or `-i`): (Optional) Drop into an interactive shell inside the build container upon failure or successful completion. This is useful for debugging build issues or inspecting artifacts.
- `--artifacts=PATTERN`: (Optional) A glob pattern to filter which build artifacts are exported to the host. For example, `--artifacts="*.rpm"` or `--artifacts="proftpd-*.deb"`. If not specified, the driver's default pattern is used.
- `--patch=PATH`: (Optional) Path to a `.patch` file, a `shipfile.py`, or a directory containing a `shipfile.py`. This allows you to run the build command from any directory without needing to be in the project root. If a `.patch` file is provided, its content is used directly. If a Shipfile or directory is provided, Shipyard loads the project and exports the patch.

## Building a Package

To build a package, navigate to your shipyard project directory (where `shipfile.py` resides) and run:

```bash
# Build proftpd on Debian Bookworm
shipyard build debian:bookworm proftpd

# Build proftpd on Rocky Linux 9
shipyard build rockylinux:9 proftpd

# Build proftpd on Arch Linux
shipyard build archlinux:base-devel proftpd
```

### Running from Any Directory

You can also run the build command from any directory by specifying the `--patch` flag:

```bash
# Build using a specific patch file
shipyard build rockylinux:9 proftpd --patch=/path/to/my.patch

# Build using a Shipfile in another directory
shipyard build debian:bookworm proftpd --patch=../my-project/shipfile.py
```

### Filtering Artifacts

To export only specific files, use the `--artifacts` flag:

```bash
# Export only RPM files matching "proftpd*"
shipyard build rockylinux:9 proftpd --artifacts="proftpd*.rpm"
```

Shipyard will:
1. Spin up a container based on the specified image.
2. Install necessary build dependencies and tools.
3. Fetch the source code for the specified package.
4. Apply the patches managed by Shipyard (or the provided patch file).
5. Build the package.
6. Export the resulting artifacts (e.g., `.deb`, `.rpm`, `.pkg.tar.zst`) to a `build-output` directory on your host.

## Troubleshooting the Build

If a build fails, you can use the `--interactive` flag to drop into a shell inside the container to investigate the state of the build.

```bash
shipyard build debian:bookworm proftpd --interactive
```

If the build fails, you will be dropped into a shell with the source code prepared and patches applied (if possible), allowing you to manually run build commands and diagnose the error. If the build succeeds, you will be dropped into a shell where you can inspect the built artifacts before they are exported.
