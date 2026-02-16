from .base import DistroDriver
import dagger

class ArchDriver(DistroDriver):
    def setup(self, container: dagger.Container) -> dagger.Container:
        return (
            container
            .with_exec(["pacman", "-Syu", "--noconfirm"])
            .with_exec(["pacman", "-S", "--noconfirm", "base-devel", "git", "devtools", "vim"])
            # Setup builder user since makepkg cannot run as root
            .with_exec(["useradd", "-m", "builder"])
            .with_exec(["/bin/sh", "-c", "echo 'builder ALL=(ALL) NOPASSWD: ALL' >> /etc/sudoers"])
            .with_exec(["mkdir", "-p", "/tmp/build"])
            .with_exec(["chown", "builder:builder", "/tmp/build"])
        )

    def install_build_deps(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_user("builder")
            .with_workdir("/tmp/build")
            # Clone the package repository
            # We use git directly or pkgctl. pkgctl is in devtools.
            # pkgctl repo clone proftpd
            .with_exec(["pkgctl", "repo", "clone", "--protocol", "https", package])
            .with_workdir(f"/tmp/build/{package}")
            # Install deps and prepare sources (extract + prepare())
            # -s: syncdeps (install missing deps)
            # -o: nobuild (download, extract, prepare)
            # --noconfirm: do not ask
            .with_exec(["makepkg", "-so", "--noconfirm"])
        )

    def prepare_source(self, container: dagger.Container, package: str) -> dagger.Container:
        # Preparation is done in install_build_deps via makepkg -o
        return container

    async def apply_patch(self, container: dagger.Container, patch_content: str, package: str) -> dagger.Container:
        # We assume the source is extracted in src/
        # There might be multiple directories in src/, but typically one main source dir.
        # We apply patch to the source directory.
        
        # We need to write the patch file first
        patch_path = f"/tmp/build/{package}/shipyard.patch"
        
        container = container.with_new_file(patch_path, patch_content)
        
        # We need to find the source dir. 
        # Usually makepkg extracts to src/$pkgname-$pkgver.
        
        return (
            container
            .with_workdir(f"/tmp/build/{package}/src")
            # We use a shell to find the directory and apply patch
            .with_exec(["/bin/bash", "-c", "cd */ && patch -p1 < ../../shipyard.patch"])
        )

    def get_artifact_pattern(self, package: str) -> str:
        return r".*\.pkg\.tar\.zst$"

    def get_artifact_dir(self) -> str:
        return "/tmp/build/artifacts"

    # Override build to move artifacts
    def build(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_user("builder")
            .with_workdir(f"/tmp/build/{package}")
            .with_exec(["makepkg", "-e", "--noconfirm"])
            .with_exec(["mkdir", "-p", "/tmp/build/artifacts"])
            .with_exec(["/bin/bash", "-c", "mv *.pkg.tar.zst /tmp/build/artifacts/"])
        )
