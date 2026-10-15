from .base import DistroDriver
import dagger
import re

class RPMDriver(DistroDriver):
    """Generic RPM based distro (Fedora, RHEL, AmazonLinux...). Distros that need
    their repos fixed up before packages can be installed subclass this and override enable_repos"""
    def enable_repos(self, container: dagger.Container) -> dagger.Container:
        return container

    def major_version(self) -> int:
        """Major release of the image: rockylinux:9.3 -> 9"""
        m = re.search(r":(\d+)", self.image)
        return int(m.group(1)) if m else 0

    def setup(self, container: dagger.Container) -> dagger.Container:
        return (
            self.enable_repos(container)
            .with_exec(["yum", "update", "-y"])
            .with_exec([
                "yum", "install", "-y",
                "gcc", "rpmdevtools", "yum-utils", "make",
                "nc", "vim", "git"
            ])
        )

    def install_build_deps(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_workdir("/tmp/build")
            .with_exec(["yum-builddep", "--skip-broken", "-y", package])
            .with_exec(["/bin/sh", "-c", "echo '%_topdir      /tmp/build' > ~/.rpmmacros"])
            .with_exec(["yumdownloader", "--source", package])
            .with_exec(["/bin/sh", "-c", "rpm -ivh *.src.rpm"])
            # Unpack the source and run the spec's own prep, leaving a clean tree in BUILD/
            .with_exec(["/bin/sh", "-c", "rpmbuild -bp SPECS/*.spec"])
        )

    def prepare_source(self, container: dagger.Container, package: str) -> dagger.Container:
        # RPM source preparation is done in install_build_deps (rpmbuild -bp)
        return container

    async def apply_patch(self, container: dagger.Container, patch_content: str, package: str) -> dagger.Container:
        specs = [s for s in await container.directory("/tmp/build/SPECS").entries() if s.endswith(".spec")]
        if not specs:
            raise ValueError("No SPEC file found in /tmp/build/SPECS")

        spec_path = f"/tmp/build/SPECS/{specs[0]}"
        content = await container.file(spec_path).contents()
        return (
            container
            .with_new_file(spec_path, self.patch_spec_file(content, package))
            .with_new_file(f"/tmp/build/SOURCES/{package}.patch", patch_content)
        )

    def patch_spec_file(self, content: str, package: str) -> str:
        """Register <package>.patch in the spec and make %prep apply it"""
        lines = content.splitlines()

        def last_line(regex: str) -> int:
            """Index of the last line matching regex, or -1"""
            return max((i for i, l in enumerate(lines) if re.match(regex, l.strip())), default=-1)

        numbers = [int(m.group(1)) for l in lines if (m := re.match(r"Patch(\d+)\s*:", l))]
        number = max(numbers, default=-1) + 1

        # The Patch: line goes after the last patch, or after the last source if there are no patches
        idx = last_line(r"Patch\d+\s*:")
        if idx < 0:
            idx = last_line(r"Source\d*\s*:")
        if idx < 0:
            raise ValueError(f"Cannot find where to add a Patch to the spec for {package}")
        lines.insert(idx + 1, f"Patch{number}: {package}.patch")

        # %autosetup applies every Patch: by itself
        if not any(l.strip().startswith("%autosetup") for l in lines):
            idx = last_line(r"%patch")
            if idx < 0:
                idx = last_line(r"%(setup|autosetup)")
            if idx < 0:
                raise ValueError(f"Cannot find %setup in the spec for {package}")
            # Newer rpm uses '%patch -P 1 -p1', older uses '%patch1 -p1'
            if "-P " in lines[idx]:
                lines.insert(idx + 1, f"%patch -P {number} -p1")
            else:
                lines.insert(idx + 1, f"%patch{number} -p1")
        return "\n".join(lines) + "\n"

    def build(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_workdir("/tmp/build")
            .with_exec(["/bin/sh", "-c", "rpmbuild --define 'debug_package %{nil}' --nocheck -bb SPECS/*.spec"])
        )

    def get_artifact_pattern(self, package: str) -> str:
        return r".*\.rpm$"

    def get_artifact_dir(self) -> str:
        return "/tmp/build/RPMS"

    async def get_source_dir(self, container: dagger.Container, package: str) -> str:
        # After rpmbuild -bp, source is in /tmp/build/BUILD/<pkg>-<version>
        out = await container.with_workdir("/tmp/build/BUILD").with_exec(["ls", "-F"]).stdout()
        for line in out.splitlines():
            if line.endswith("/"):
                return f"/tmp/build/BUILD/{line.rstrip('/')}"
        return "/tmp/build/BUILD"


class RockyDriver(RPMDriver):
    def enable_repos(self, container: dagger.Container) -> dagger.Container:
        # The -devel packages that build deps need live in an extra repo
        repo = "powertools" if self.major_version() == 8 else "crb"
        return (
            container
            .with_exec(["dnf", "install", "-y", "epel-release", "dnf-plugins-core"])
            .with_exec(["dnf", "config-manager", "--set-enabled", repo])
        )


class CentOSDriver(RPMDriver):
    def enable_repos(self, container: dagger.Container) -> dagger.Container:
        # CentOS 7 and 8 are EOL, their packages only exist on the vault now
        container = container.with_exec([
            "/bin/sh", "-c",
            "sed -i -e 's/mirrorlist/#mirrorlist/g' "
            "-e 's|#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' "
            "/etc/yum.repos.d/CentOS-*"
        ])
        if self.major_version() == 8:
            container = container.with_exec(["dnf", "install", "-y", "dnf-plugins-core"]).with_exec(
                ["dnf", "config-manager", "--set-enabled", "powertools"]
            )
        return container
