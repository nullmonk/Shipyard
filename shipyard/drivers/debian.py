from .base import DistroDriver
import dagger

class DebianDriver(DistroDriver):
    def setup(self, container: dagger.Container) -> dagger.Container:
        return (
            container
            #TODO Can I pull the timezone from the local environment here?
            .with_exec(["ln", "-fs", "/usr/share/zoneinfo/America/New_York", "/etc/localtime"])
            # For EOL Debian releases (buster and older), deb.debian.org no longer hosts
            # Release files — rewrite sources to archive.debian.org before anything else.
            .with_exec([
                "/bin/bash", "-c",
                ". /etc/os-release; "
                "case \"${VERSION_CODENAME:-}\" in "
                "  buster|stretch|jessie|wheezy|squeeze) "
                "    printf 'deb http://archive.debian.org/debian %s main\\n"
                "deb-src http://archive.debian.org/debian %s main\\n"
                "deb http://archive.debian.org/debian-security %s/updates main\\n"
                "deb-src http://archive.debian.org/debian-security %s/updates main\\n' "
                "      \"$VERSION_CODENAME\" \"$VERSION_CODENAME\" "
                "      \"$VERSION_CODENAME\" \"$VERSION_CODENAME\" > /etc/apt/sources.list; "
                "    echo 'Acquire::Check-Valid-Until \"false\";' "
                "      > /etc/apt/apt.conf.d/99no-check-valid-until; "
                "    find /etc/apt/sources.list.d -type f -delete 2>/dev/null || true; "
                "    ;; "
                "esac"
            ])
            # Ensure source repos (Handle both legacy sources.list and modern sources.list.d)
            .with_exec([
                "/bin/bash", "-c",
                "if [ -f /etc/apt/sources.list ]; then "
                "  sed -i 's/# deb-src/deb-src/' /etc/apt/sources.list; "
                "  if ! grep -q 'deb-src' /etc/apt/sources.list; then "
                "    src=$(grep -E '^\\s*deb ' /etc/apt/sources.list | head -n 1); "
                "    if [ -n \"$src\" ]; then echo \"${src/deb /deb-src }\" >> /etc/apt/sources.list; fi; "
                "  fi; "
                "fi"
            ])
            # Newer ubuntus install the sources here instead of in sources.list
            .with_exec(["find", "/etc/apt/sources.list.d", "-type", "f", "-exec", "sed", "-i", "s/Types:/Types: deb-src/", "{}", ";"])

            .with_exec(["apt-get", "update", "-qq"])
            .with_exec([
                "apt-get", "install", "-qq", "-y",
                "gcc", "devscripts", "quilt", "build-essential",
                "vim", "iproute2", "nmap", "git"
            ])
            .with_exec(["mkdir", "-p", "/tmp/build/"])
        )

    def install_build_deps(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_workdir("/tmp/build")
            .with_exec(["apt-get", "build-dep", "-q", "-y", package])
            # Try to install the package itself if available, ignore error if not
            .with_exec(["/bin/sh", "-c", f"(apt-get install -q -y {package} || echo -n)"])
            .with_exec(["apt-get", "source", "-qq", package])
        )

    def prepare_source(self, container: dagger.Container, package: str) -> dagger.Container:
         # Debian apt-get source unpacks automatically in current dir
         return container

    async def apply_patch(self, container: dagger.Container, patch_content: str, package: str) -> dagger.Container:
        return (
            container
            .with_new_file("/tmp/build/shipyard.patch", patch_content)
            .with_workdir("/tmp/build")
            .with_exec(["/bin/bash", "-c", "cd */ && quilt import /tmp/build/shipyard.patch"])
            .with_exec(["/bin/bash", "-c", "cd */ && quilt push"])
            .with_exec(["/bin/bash", "-c", "cd */ && quilt refresh"])
        )

    def build(self, container: dagger.Container, package: str) -> dagger.Container:
        # Build inside the source directory.
        # Since we don't know the exact name (e.g. package-version), we use shell globbing to enter it.
        # We assume there is only one directory in /tmp/build matching the pattern after apt-get source.
        
        # Env vars for skipping tests/checks
        return (
            container
            .with_env_variable("DEB_BUILD_OPTIONS", "notest nocheck")
            .with_env_variable("DEBUILD_DPKG_BUILDPACKAGE_OPTS", "-d")
            .with_workdir("/tmp/build")
            # We use bash to glob and enter the directory
            .with_exec(["/bin/bash", "-c", "cd */ && debuild --no-lintian -d -uc -us -b"])
        )

    def get_artifact_pattern(self, package: str) -> str:
        return r".*\.deb$"
    
    def get_artifact_dir(self) -> str:
        return "/tmp/build"

    async def get_source_dir(self, container: dagger.Container, package: str) -> str:
        # Debian apt-get source unpacks into a subdirectory of /tmp/build
        # We find the directory that isn't shipyard.patch and is a directory
        out = await container.with_workdir("/tmp/build").with_exec(["ls", "-F"]).stdout()
        for line in out.splitlines():
            if line.endswith("/") and line != "artifacts/":
                return f"/tmp/build/{line.rstrip('/')}"
        return "/tmp/build"
