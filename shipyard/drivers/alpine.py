from .base import DistroDriver
import dagger

APORTS = "https://gitlab.alpinelinux.org/alpine/aports.git"

class AlpineDriver(DistroDriver):
    """Alpine builds from an APKBUILD in the aports tree. abuild is run as root (-F)
    since the container is thrown away anyway"""
    def setup(self, container: dagger.Container) -> dagger.Container:
        return (
            container
            .with_exec(["apk", "add", "--no-cache", "alpine-sdk", "git", "patch"])
            # abuild refuses to build without a signing key, and apk must trust it.
            # (abuild-keygen -i would do the install, but it needs doas)
            .with_exec(["abuild-keygen", "-a", "-n"])
            .with_exec(["/bin/sh", "-c", "cp /root/.abuild/*.pub /etc/apk/keys/"])
            .with_exec(["mkdir", "-p", "/tmp/build"])
        )

    def install_build_deps(self, container: dagger.Container, package: str) -> dagger.Container:
        # Check out only this package from the aports branch matching the release
        # (3.20.3 -> 3.20-stable, edge and alpha releases -> master)
        get_aport = (
            'rel=$(cat /etc/alpine-release); '
            'case "$rel" in *_*|*edge*) branch=master;; *) branch=$(echo "$rel" | cut -d. -f1,2)-stable;; esac; '
            f'git clone -q --depth 1 --branch "$branch" --filter=blob:none --sparse {APORTS} /tmp/aports && '
            'cd /tmp/aports && '
            f'git sparse-checkout set main/{package} community/{package} testing/{package} && '
            f'cp -r $(ls -d main/{package} community/{package} testing/{package} 2>/dev/null | head -n 1) /tmp/build/{package}'
        )
        return (
            container
            .with_exec(["/bin/sh", "-c", get_aport])
            .with_workdir(f"/tmp/build/{package}")
            .with_exec(["abuild", "-F", "deps"])
            # Download, unpack and prepare (the distro's own patches) the source into src/
            .with_exec(["abuild", "-F", "fetch", "unpack", "prepare"])
        )

    def prepare_source(self, container: dagger.Container, package: str) -> dagger.Container:
        return container

    async def apply_patch(self, container: dagger.Container, patch_content: str, package: str) -> dagger.Container:
        # Add the patch to the APKBUILD sources, abuild applies every *.patch it finds there
        # when it prepares the source. Tests are skipped like the other distros.
        return (
            container
            .with_workdir(f"/tmp/build/{package}")
            .with_new_file(f"/tmp/build/{package}/shipyard.patch", patch_content)
            .with_exec(["sed", "-i", 's/^source="/source="\\n\\tshipyard.patch\\n\\t/', "APKBUILD"])
            .with_exec(["/bin/sh", "-c", "echo 'options=\"$options !check\"' >> APKBUILD"])
            .with_exec(["abuild", "-F", "checksum"])
        )

    def build(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_workdir(f"/tmp/build/{package}")
            .with_exec(["abuild", "-F", "-r"])
        )

    def get_artifact_pattern(self, package: str) -> str:
        return r".*\.apk$"

    def get_artifact_dir(self) -> str:
        return "/root/packages"

    async def get_source_dir(self, container: dagger.Container, package: str) -> str:
        # abuild unpacks to src/<pkgname>-<pkgver>
        src = f"/tmp/build/{package}/src"
        out = await container.with_workdir(src).with_exec(["ls", "-F"]).stdout()
        for line in out.splitlines():
            if line.endswith("/"):
                return f"{src}/{line.rstrip('/')}"
        return src
