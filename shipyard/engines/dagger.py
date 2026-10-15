"""
The build pipeline:

    1. distro container: install build deps, fetch the distro source, snapshot it in git
    2. shipyard container: run the Shipfile's CodePatches on a copy of that source
    3. distro container: take the patched source back and `git diff` it into a patch
    4. distro container: reset the tree, apply the patch the distro's way, build the package
"""
import os
import re
import sys
import dagger

from shipyard.drivers.base import DistroDriver
from shipyard.drivers.debian import DebianDriver
from shipyard.drivers.rpm import RPMDriver, RockyDriver, CentOSDriver
from shipyard.drivers.arch import ArchDriver

# Package root, mounted into the shipyard container
SHIPYARD_PKG = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

RUNNER_IMAGE = "python:3-slim"
GIT = ["git", "-c", "user.name=shipyard", "-c", "user.email=shipyard@localhost"]

def get_driver(image: str) -> DistroDriver:
    if any(x in image for x in ["debian", "ubuntu", "linuxmint", "kali"]):
        return DebianDriver(image)
    if "rocky" in image:
        return RockyDriver(image)
    if "centos" in image:
        return CentOSDriver(image)
    if any(x in image for x in ["redhat", "fedora", "amazonlinux"]):
        return RPMDriver(image)
    if "archlinux" in image:
        return ArchDriver(image)
    raise ValueError(f"Unsupported image: {image}")

def snapshot_source(ctr: dagger.Container, source_dir: str) -> dagger.Container:
    """Commit the pristine source so the shipfile's changes can be diffed out later"""
    return (
        ctr.with_workdir(source_dir)
        .with_exec(["git", "init", "-q"])
        .with_exec(["git", "add", "-A", "-f"])
        .with_exec(GIT + ["commit", "-q", "-m", "pristine"])
    )

def run_shipfile(client: dagger.Client, source: dagger.Directory, shipfile_dir: str, source_name: str, version: str) -> dagger.Directory:
    """Run the shipfile on the source inside the shipyard container, return the patched source"""
    shipfile = client.host().directory(shipfile_dir, exclude=["sources/", "__pycache__/", "build-output/"])
    shipyard = client.host().directory(SHIPYARD_PKG, exclude=["__pycache__/"])
    args = ["python", "-m", "shipyard.runner", "/ship", "/work/" + source_name]
    if version:
        args.append(version)
    runner = (
        client.container().from_(RUNNER_IMAGE)
        .with_directory("/opt/shipyard/shipyard", shipyard)
        .with_env_variable("PYTHONPATH", "/opt/shipyard")
        .with_env_variable("PYTHONDONTWRITEBYTECODE", "1")
        .with_directory("/ship", shipfile)
        .with_directory("/work/" + source_name, source)
        .with_exec(args)
    )
    return runner.directory("/work/" + source_name)

def generate_patch(ctr: dagger.Container, source_dir: str) -> dagger.File:
    """Diff the patched working tree against the pristine commit"""
    return ctr.with_workdir(source_dir).with_exec(
        ["/bin/sh", "-c", "git add -A -f && git diff --cached --binary > /tmp/shipyard.patch"]
    ).file("/tmp/shipyard.patch")

async def build_package(image: str, package: str, patch_content: str = "", output_dir: str = "builds", interactive: bool = False, artifacts: str = "", shipyard_dir: str = ".", version: str = ""):
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        driver = get_driver(image)
        print(f"[*] Starting build for {package} on {image}")

        ctr = None
        ctr_pre_build = None
        try:
            # 1. distro container with the build deps and the source
            ctr = client.container().from_(image)
            ctr = driver.setup(ctr)
            ctr = driver.install_build_deps(ctr, package)
            ctr = driver.prepare_source(ctr, package)
            source_dir = await driver.get_source_dir(ctr, package)
            source_name = os.path.basename(source_dir)
            ctr = snapshot_source(ctr, source_dir)

            if shipyard_dir:
                # 2. shipyard container runs the shipfile on the source
                print(f"[*] Running shipfile from {shipyard_dir} on {source_name}")
                patched = run_shipfile(client, ctr.directory(source_dir), shipyard_dir, source_name, version)

                # 3. patched source goes back into the distro container and becomes a patch
                ctr = ctr.with_directory(source_dir, patched)
                patch_content = await generate_patch(ctr, source_dir).contents() + patch_content
            if not patch_content.strip():
                raise ValueError("The shipfile did not change the source, there is nothing to build")
            print(f"[*] Generated patch ({len(patch_content.splitlines())} lines)")

            # 4. restore the pristine tree and let the distro apply the patch itself
            ctr = (
                ctr.with_workdir(source_dir)
                .with_exec(["git", "reset", "-q", "--hard"])
                .with_exec(["git", "clean", "-q", "-fdx"])
                .with_exec(["rm", "-rf", ".git"])
            )
            ctr = await driver.apply_patch(ctr, patch_content, package)

            ctr_pre_build = ctr
            ctr = driver.build(ctr, package)
            os.makedirs(output_dir, exist_ok=True)
            artifact_pattern = artifacts if artifacts else driver.get_artifact_pattern(package)
            src_dir = driver.get_artifact_dir()
            print(f"[*] listing artifacts in {src_dir}...")
            files = await driver.list_artifacts(ctr)
            matches = [f for f in files if re.search(artifact_pattern, f)]
            if not matches:
                raise ValueError(f"The build produced no artifacts matching '{artifact_pattern}' in {src_dir}")
            else:
                print(f"[*] Found {len(matches)} artifacts matching '{artifact_pattern}'")
                ctr = ctr.with_exec(["mkdir", "-p", "/tmp/artifacts"])
                ctr = ctr.with_new_file("/tmp/artifacts_list", "\n".join(matches))
                copy_cmd = f"cd {src_dir} && tr '\\n' '\\0' < /tmp/artifacts_list | xargs -0 cp -t /tmp/artifacts/"
                ctr = ctr.with_exec(["/bin/bash", "-c", copy_cmd])
                await ctr.directory("/tmp/artifacts").export(output_dir)

                print(f"[+] Build complete. Artifacts exported to {output_dir}")
                for root, dirs, files in os.walk(output_dir):
                    for file in files:
                        print(f"  - {file}")

            if interactive:
                print("[*] Build successful. Dropping into interactive shell...")
                await ctr.terminal().sync()

        except Exception as e:
            print(f"[!] Build failed: {e}")
            if interactive:
                target_ctr = ctr_pre_build or ctr
                if target_ctr:
                    print("[*] Dropping into interactive shell...")
                    await target_ctr.terminal().sync()
                else:
                    print("[!] No container available to drop into.")
            else:
                raise e
