from .base import DistroDriver
import dagger
import os
import re

class RPMDriver(DistroDriver):
    """Generic RPM based distro (Fedora, RHEL, AmazonLinux...). Distros that need
    their repos fixed up before packages can be installed subclass this and override enable_repos"""
    # rpmbuild's top directory, where SPECS, SOURCES, BUILD and RPMS live
    topdir = "/tmp/build"

    # Extra arguments for rpmbuild
    rpmbuild_args = ""

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
            .with_workdir(self.topdir)
            .with_exec(["yum-builddep", "--skip-broken", "-y", package])
            .with_exec(["/bin/sh", "-c", f"echo '%_topdir      {self.topdir}' > ~/.rpmmacros"])
            .with_exec(["yumdownloader", "--source", package])
            .with_exec(["/bin/sh", "-c", "rpm -ivh *.src.rpm"])
            # Unpack the source and run the spec's own prep, leaving a clean tree in BUILD/
            .with_exec(["/bin/sh", "-c", f"rpmbuild {self.rpmbuild_args} -bp SPECS/*.spec"])
        )

    def prepare_source(self, container: dagger.Container, package: str) -> dagger.Container:
        # RPM source preparation is done in install_build_deps (rpmbuild -bp)
        return container

    async def apply_patch(self, container: dagger.Container, patch_content: str, package: str) -> dagger.Container:
        specs = [s for s in await container.directory(f"{self.topdir}/SPECS").entries() if s.endswith(".spec")]
        if not specs:
            raise ValueError(f"No SPEC file found in {self.topdir}/SPECS")

        spec_path = f"{self.topdir}/SPECS/{specs[0]}"
        content = await container.file(spec_path).contents()

        # rpm 4.20 removed the '%patchN' macro, and rpm before 4.14 does not know '%patch -P N'
        version = (await container.with_exec(["rpm", "--version"]).stdout()).split()[-1]
        legacy_macro = tuple(int(i) for i in version.split(".")[:2]) < (4, 14)
        return (
            container
            .with_new_file(spec_path, self.patch_spec_file(content, package, legacy_macro))
            .with_new_file(f"{self.topdir}/SOURCES/{package}.patch", patch_content)
        )

    def patch_spec_file(self, content: str, package: str, legacy_macro: bool = False) -> str:
        """Register <package>.patch in the spec and make %prep apply it"""
        lines = content.splitlines()

        def matching_lines(regex: str) -> list:
            return [i for i, l in enumerate(lines) if re.match(regex, l.strip())]

        def last_line(regex: str) -> int:
            """Index of the last line matching regex, or -1"""
            return max(matching_lines(regex), default=-1)

        numbers = [int(m.group(1)) for l in lines if (m := re.match(r"Patch(\d+)\s*:", l))]
        number = max(numbers, default=-1) + 1

        # The Patch: line goes at the end of the preamble. Next to the other patches it could
        # sit inside a conditional that does not apply to this build.
        # (the first %description, later ones belong to sub packages)
        descriptions = matching_lines(r"%description")
        if not descriptions:
            raise ValueError(f"Cannot find %description in the spec for {package}")
        lines.insert(descriptions[0], f"Patch{number}: {package}.patch")

        # %autosetup and %autopatch apply every Patch: by themselves
        if not any(l.strip().startswith(("%autosetup", "%autopatch")) for l in lines):
            idx = last_line(r"%patch")
            if idx < 0:
                idx = last_line(r"%(setup|autosetup)")
            if idx < 0:
                raise ValueError(f"Cannot find %setup in the spec for {package}")
            if legacy_macro:
                lines.insert(idx + 1, f"%patch{number} -p1")
            else:
                lines.insert(idx + 1, f"%patch -P {number} -p1")
        return "\n".join(lines) + "\n"

    def build(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_workdir(self.topdir)
            .with_exec(["/bin/sh", "-c", f"rpmbuild --define 'debug_package %{{nil}}' --nocheck {self.rpmbuild_args} -bb SPECS/*.spec"])
        )

    def get_artifact_pattern(self, package: str) -> str:
        return r".*\.rpm$"

    def get_artifact_dir(self) -> str:
        return f"{self.topdir}/RPMS"

    async def get_source_dir(self, container: dagger.Container, package: str) -> str:
        # After rpmbuild -bp, source is in <topdir>/BUILD/<pkg>-<version>
        build = f"{self.topdir}/BUILD"
        out = await container.with_workdir(build).with_exec(["ls", "-F"]).stdout()
        dirs = [l.rstrip("/") for l in out.split() if l.endswith("/")]
        if not dirs:
            return build
        name = dirs[0]

        # Newer rpm wraps that in a <pkg>-<version>-build directory with the source inside it
        if name.endswith("-build"):
            out = await container.with_workdir(f"{build}/{name}").with_exec(["ls", "-F"]).stdout()
            inner = name[:-len("-build")]
            if inner + "/" in out.split():
                return f"{build}/{name}/{inner}"
        return f"{build}/{name}"


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


class SUSEDriver(RPMDriver):
    """openSUSE (Leap, Tumbleweed) and SUSE. Same rpm build as everything else,
    but zypper fetches the source package and rpm's top directory is fixed"""
    topdir = "/usr/src/packages"
    # Some BuildRequires do not exist in the container's repos (we skipped them), rpmbuild would refuse to start
    rpmbuild_args = "--nodeps"

    def setup(self, container: dagger.Container) -> dagger.Container:
        return (
            container
            # The source package lives in a repo that is disabled by default
            .with_exec(["zypper", "-n", "--gpg-auto-import-keys", "mr", "--enable", "repo-source"])
            .with_exec(["zypper", "-n", "--gpg-auto-import-keys", "refresh"])
            .with_exec([
                "zypper", "-n", "install", "--no-recommends",
                "gcc", "make", "patch", "tar", "gzip", "rpm-build", "git",
                # Macros that specs use (%sysusers_requires, %service_add_pre...)
                "systemd-rpm-macros", "sysuser-tools"
            ])
        )

    def install_build_deps(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_workdir(self.topdir)
            # Put the source package into SPECS and SOURCES. zypper gives up on the first build
            # dependency it cannot find (unlike yum-builddep --skip-broken), so install the
            # spec's BuildRequires one at a time and skip the ones that do not exist. The
            # '-mini' packages are cut down builds of a library, use the full one instead.
            .with_exec(["zypper", "-n", "--gpg-auto-import-keys", "source-install", "--no-build-deps", package])
            .with_exec([
                "/bin/sh", "-c",
                "for dep in $(rpmspec -q --buildrequires SPECS/*.spec | awk '{print $1}' | sort -u); do "
                "zypper -n install --no-recommends \"$dep\" >/dev/null 2>&1 || "
                "zypper -n install --no-recommends \"$(echo \"$dep\" | sed s/-mini//)\" >/dev/null 2>&1 || "
                "echo \"skipping $dep\"; done"
            ])
            .with_exec(["/bin/sh", "-c", f"rpmbuild {self.rpmbuild_args} -bp SPECS/*.spec"])
        )
