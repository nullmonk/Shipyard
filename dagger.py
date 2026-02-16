import sys
import anyio
import dagger
import argparse
import re

async def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--image", required=True)
    parser.add_argument("--package", required=True)
    parser.add_argument("--patch", required=True)
    args = parser.parse_args()

    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        await build(client, args.image, args.package, args.patch)

async def build(client: dagger.Client, image: str, package: str, patch: str):
    """
    Builds a package with a given patch on a given image.
    """
    print(f"Building {package} on {image} with patch {patch}")

    build_mode = ""
    ctr = None
    if re.search(r"(debian:|ubuntu:|linuxmintd|kalilinux)", image):
        print("Using Debian-based image")
        build_mode = "deb"
        ctr = (
            client.container()
            .from_(image)
            .with_exec(["apt-get", "update", "-qq"])
            .with_exec(
                [
                    "apt-get",
                    "install",
                    "-qq",
                    "-y",
                    "gcc",
                    "devscripts",
                    "quilt",
                    "build-essential",
                    "vim",
                    "iproute2",
                    "python3-pip",
                    "nmap",
                    "git",
                ]
            )
            .with_workdir("/tmp/build")
            .with_exec(["apt-get", "build-dep", "-q", "-y", package])
            .with_exec(["apt-get", "source", "-qq", package])
        )
    elif re.search(r"(redhat/|centos:|rockylinux:|fedora:|amazonlinux:)", image):
        print("Using RHEL-based image")
        build_mode = "rpm"
        ctr = (
            client.container()
            .from_(image)
            .with_exec(["yum", "update", "-y"])
            .with_exec(
                [
                    "yum",
                    "install",
                    "-y",
                    "gcc",
                    "rpmdevtools",
                    "yum-utils",
                    "make",
                    "nc",
                    "vim",
                    "python3",
                    "python3-pip",
                    "git",
                ]
            )
            .with_workdir("/tmp/build")
            .with_exec(["yum-builddep", "--skip-broken", "-y", package])
            .with_exec(["yumdownloader", "--source", package])
            .with_exec(["/bin/sh", "-c", "rpm -ivh *.src.rpm"])
            .with_exec(["/bin/sh", "-c", "rpmbuild -bp SPECS/*.spec"])
        )
    elif re.search(r"(archlinux:)", image):
        print("Using Arch-based image")
        build_mode = "arch"
        ctr = (
            client.container()
            .from_(image)
            .with_exec(["pacman", "-Syu", "--noconfirm"])
            .with_exec(
                [
                    "pacman",
                    "-S",
                    "--noconfirm",
                    "python",
                    "python-pip",
                    "base-devel",
                ]
            )
        )
    else:
        raise ValueError(f"Unsupported image: {image}")

    # Install shipyard
    src = client.host().directory(".")
    ctr = (
        ctr
        .with_exec(["python3", "-m", "pip", "config", "set", "global.break-system-packages", "true"], ignore_exit_code=True)
        .with_exec(["python3", "-m", "pip", "install", "dataclasses", "setuptools"], ignore_exit_code=True)
        .with_directory("/src", src)
        .with_workdir("/src")
        .with_exec(["python3", "-m", "pip", "install", "."])
    )

    # Run the build
    ctr = (
        ctr
        .with_workdir("/tmp/build")
        .with_exec(["shipyard-build", "gen", f"/src/{patch}", f"/tmp/build/{package}.patch", "--package", package])
        .with_exec(["shipyard-build", "apply", f"/tmp/build/{package}.patch", "--package", package])
        .with_exec(["shipyard-build", "build", package])
    )

    # Save artifacts
    if build_mode == "deb":
        await ctr.directory(f"/tmp/build").glob(f"*{package}*_amd64.deb").export("./build")
    elif build_mode == "rpm":
        await ctr.directory(f"/tmp/build/RPMS").glob(f"**/*{package}*.rpm").export("./build")

    # print the output
    out = await ctr.stdout()
    print(out)

if __name__ == "__main__":
    anyio.run(main)
