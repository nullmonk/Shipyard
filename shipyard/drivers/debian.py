from .base import DistroDriver
import dagger

class DebianDriver(DistroDriver):
    def setup(self, container: dagger.Container) -> dagger.Container:
        return (
            container
            #TODO Can I pull the timezone from the local environment here?
            .with_exec(["ln", "-fs", "/usr/share/zoneinfo/America/New_York", "/etc/localtime"])
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
                "vim", "iproute2", "python3-pip", "nmap", "git"
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
        # We need to find the source directory first.
        # `apt-get source` creates a directory like `package-version`.
        # Since we are in /tmp/build, we can use `cd */` trick inside `bash`.
        # But `quilt` needs to run *inside* that directory.
        
        # We also need to copy the patch file *into* that directory for quilt to see it relative to source root?
        # Actually `quilt import` takes a path.
        
        # So:
        # 1. Write patch to /tmp/build/shipyard.patch
        # 2. Run quilt commands inside the source dir.
        
        # Note: We use `bash -c "cd */ && quilt ..."` because we don't know the dir name.
        
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
