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
        # Usually it's the only directory in src/ that is not a symlink?
        # Or we can just try to apply patch in src/ using -p0 or -p1?
        # Usually makepkg extracts to src/$pkgname-$pkgver.
        
        # Let's try to apply patch in the src directory, assuming the patch is relative to the source root.
        # We'll use find to locate the source directory if needed, or just try to patch from src/ base.
        # Typically patches are -p1 relative to the source directory.
        
        return (
            container
            .with_workdir(f"/tmp/build/{package}/src")
            # We use a shell to find the directory and apply patch
            .with_exec(["/bin/bash", "-c", "cd */ && patch -p1 < ../../shipyard.patch"])
        )

    def build(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_user("builder")
            .with_workdir(f"/tmp/build/{package}")
            # -e: noextract (skip extract and prepare, just build and package)
            # --noconfirm
            .with_exec(["makepkg", "-e", "--noconfirm"])
        )

    def get_artifact_pattern(self, package: str) -> str:
        return r".*\.pkg\.tar\.zst$"

    def get_artifact_dir(self) -> str:
        # Artifacts are in the package dir
        # We don't know the exact path easily without package context, 
        # but the caller of this method (dagger.py) uses the return value to find files.
        # However, `build_package` in dagger.py expects a fixed directory to glob from.
        # The Arch driver builds in /tmp/build/{package}.
        # So we can't return a constant string if it depends on {package} unless we store package in self?
        # But get_artifact_dir doesn't take package arg in base.py?
        # Wait, let me check base.py.
        # get_artifact_dir(self) -> str.
        
        # Issue: Debian and RPM drivers use a fixed /tmp/build.
        # Arch uses /tmp/build/{package}.
        # I should probably change the interface or adapt Arch driver to move artifacts to /tmp/build.
        
        # Let's adapt Arch driver to move artifacts to /tmp/build at the end of build?
        # Or I can modify base.py? No, I should stick to interface.
        
        # Let's modify the build() method to move artifacts to /tmp/build/RPMS-like directory?
        # Or just /tmp/build/output
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
