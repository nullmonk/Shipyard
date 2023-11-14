# Building Patches

Shipyard uses [Earthly](https://earthly.dev) to build packages. Shipyard provides a single (albiet complicated) [Earthfile](../Earthfile) to allow easy building of packages using a single patchfile or using a shipfile.

For best results, set up an earthly satelite to build faster.

## Earthfile Arguments

Earthfile accepts several args to build packages:

1. `source` - Source docker container to build on. Supports the following versions:
    - `debian:*` (untested)
    - `ubuntu:*`
    - `linuxmind/*:*` (untested)
    - `kalilinux/*:*` (untested)
    - `centos:*`
    - `rockylinux:*` (untested)
    - `fedora:*`
    - `amazonlinux:*` (untested)
    - (WIP) `archlinux:*`

1. `package` - Package the will be build and patched
1. `export` - Pass `true` or `yes` to export the image for inspection of errors
1. `patchfile` - A patchfile to be copied into the builder and applied to the version
1. `shipfile` - A file or directory to be copied into the builder that contains a shipfile and patches

> Either `patchfile` or `shipfile` must be specified

## Building a package

Export the version that you would like to build via shipyard:

```bash
shipyard export v1.0 >> package.patch
```

Run the build job:
```bash
shipyard +build --source ubuntu:latest --package openssh-server --patchfile package.patch
```


## Troubleshooting the Build
When there are errors in the build process, you may hop into the container to check it out. Run the build job with exporting enabled:
```bash
shipyard +build --source ubuntu:latest --package openssh-server --patchfile package.patch --export true
```

Launch a shell in the container
```
docker run --rm -it openssh-server-builder bash
```
