import sys
import anyio
import dagger
import os
from shipyard.drivers.debian import DebianDriver
from shipyard.drivers.rpm import RPMDriver

async def build_package(image: str, package: str, patch_content: str, output_dir: str = "build-output"):
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # Determine driver
        if any(x in image for x in ["debian", "ubuntu", "linuxmint", "kali"]):
            driver = DebianDriver(image)
        elif any(x in image for x in ["redhat", "centos", "rocky", "fedora", "amazonlinux"]):
            driver = RPMDriver(image)
        elif "arch" in image:
             # @TODO: Implement Arch Linux driver
             raise NotImplementedError("Arch Linux support is not yet implemented in Dagger engine")
        else:
            raise ValueError(f"Unsupported image: {image}")

        print(f"[*] Starting build for {package} on {image}")

        # Pipeline
        # 1. Setup
        ctr = client.container().from_(image)
        ctr = driver.setup(ctr)
        
        # 2. Install Deps & Prepare Source
        ctr = driver.install_build_deps(ctr, package)
        
        # 3. Apply Patch
        ctr = await driver.apply_patch(ctr, patch_content, package)
        
        # 4. Build
        ctr = driver.build(ctr, package)
        
        # 5. Export Artifacts
        # Ensure output directory exists on host
        os.makedirs(output_dir, exist_ok=True)
        
        artifact_pattern = driver.get_artifact_pattern(package)
        src_dir = driver.get_artifact_dir()
        
        # We need to export matching files.
        # We can copy artifacts to a clean directory inside container and export that.
        ctr = ctr.with_exec(["mkdir", "-p", "/tmp/artifacts"])
        
        # Use find to copy, because cp with glob in shell might fail if too many arguments or no arguments?
        # Safe way: find src_dir -name pattern -exec cp {} /tmp/artifacts/ \;
        # Note: glob pattern needs to be converted to find pattern or just use shell glob
        # Shell glob is easier: cp -r src_dir/pattern /tmp/artifacts/
        
        # But src_dir might be complex glob? No, src_dir is fixed path. artifact_pattern is glob.
        # RPMDriver pattern: `**/*{package}*.rpm`. `cp` doesn't support `**` recursively usually (unless bash 4+ with globstar).
        # DebianDriver pattern: `*{package}*_amd64.deb` (flat).
        
        # Dagger `export` takes a directory.
        # Let's use `find` which is safer and standard.
        # Convert artifact_pattern `**/*.rpm` to `-name *.rpm`? No, `find` is recursive by default.
        # If pattern contains `**`, we should just use filename part for `find -name`.
        # @TODO: Improve robustness of artifact pattern matching (handle full globs)
        
        find_name = artifact_pattern.replace("**/" , "")
        
        cmd = ["find", src_dir, "-name", find_name, "-exec", "cp", "{}", "/tmp/artifacts/", ";"]
        
        ctr = ctr.with_exec(cmd)
        
        await ctr.directory("/tmp/artifacts").export(output_dir)
        
        print(f"[+] Build complete. Artifacts exported to {output_dir}")
