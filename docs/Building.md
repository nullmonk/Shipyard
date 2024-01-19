# Building Patches

Shipyard uses [Earthly](https://earthly.dev) to build packages. Shipyard provides a single (albiet complicated) [Earthfile](../Earthfile) to allow easy building of packages using a single patchfile or using a shipfile.

For best results, set up an earthly satelite to build faster.

## Earthfile Arguments

Earthfile accepts several args to build packages:

1. `image` - Source docker container to build on. Supports the following versions:
    - `debian:*` (untested)
    - `ubuntu:*`
    - `linuxmind/*:*` (untested)
    - `kalilinux/*:*` (untested)
    - `centos:*`
    - `rockylinux:*`
    - `fedora:*`
    - `amazonlinux:*` (untested)
    - (WIP) `archlinux:*`

1. `package` - Package the will be build and patched
1. `patch` - A file or directory to be copied into the builder that contains a shipfile and/or patches to be applied to the source
1. `artifacts` - defaults to `$package` - Save build artifacts with this name
1. `export` - Pass `true` or `yes` to export the image for inspection of errors/tests
1. `dev` - Install shipyard from this repo instead of from github

## Building a package
Export the version that you would like to build via shipyard:

```bash
shipyard export v1.0 >> package.patch
```

Examples of running the build job:
```bash
earthly +build --image ubuntu:20.04 --package openssh-server --patch package.patch
```
Run a build job with a shipfile (and auto version control). If your shipfile requires patches, pass the directory containing the shipfile and patches. If your shipfile only uses codepatches, you may pass just the shipfile

```bash
earthly +build --image rockylinux:8 --package openssh-server --patch openssh/
earthly +build --image fedora:39 --package openssh-server --patch openssh/Shipfile.py

# Save all artifacts matching openssh*.rpm instead of just openssh-server*.rpm
earthly +build --image fedora:39 --package openssh-server --patch openssh/Shipfile.py --artifacts openssh
```

## Troubleshooting the Build
When there are errors in the build process, you may hop into the container to check it out. Run the build job with `-i` enabled:

```bash
earthly -i +build --image ubuntu:latest --package openssh-server --patch shipfile.py
```

## Building many images at once
