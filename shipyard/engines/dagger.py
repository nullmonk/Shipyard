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
from shipyard.drivers.rpm import RPMDriver, RockyDriver, CentOSDriver, SUSEDriver
from shipyard.drivers.alpine import AlpineDriver
from shipyard.drivers.arch import ArchDriver

# Package root, mounted into the shipyard container
SHIPYARD_PKG = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

RUNNER_IMAGE = "python:3-slim"
GIT = ["git", "-c", "user.name=shipyard", "-c", "user.email=shipyard@localhost"]

def get_driver(image: str) -> DistroDriver:
    if any(x in image for x in ["debian", "ubuntu", "linuxmint", "kali"]):
        return DebianDriver(image)
    if "suse" in image:
        return SUSEDriver(image)
    if "alpine" in image:
        return AlpineDriver(image)
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
        # The tree changes owner when it is copied between containers, and git refuses those
        ctr.with_exec(["git", "config", "--global", "--add", "safe.directory", "*"])
        .with_workdir(source_dir)
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

async def find_files(container: dagger.Container, directory: str) -> list[str]:
    """List all files under directory. This is the first thing that forces the (lazy)
    build to run, so a build failure surfaces here."""
    output = await (
        container
        .with_workdir(directory)
        .with_exec(["find", ".", "-type", "f"])
        .stdout()
    )
    return [f[2:] if f.startswith("./") else f for f in map(str.strip, output.splitlines()) if f]

async def export_artifacts(ctr: dagger.Container, files: list[str], src_dir: str, artifact_pattern: str, output_dir: str) -> dagger.Container:
    """Filter files (relative to src_dir) by artifact_pattern and export the matches to output_dir on the host"""
    matches = [f for f in files if re.search(artifact_pattern, f)]
    if not matches:
        raise ValueError(f"The build produced no artifacts matching '{artifact_pattern}' in {src_dir}")
    print(f"[*] Found {len(matches)} artifacts matching '{artifact_pattern}'")
    ctr = ctr.with_exec(["mkdir", "-p", "/tmp/artifacts"])
    ctr = ctr.with_new_file("/tmp/artifacts_list", "\n".join(matches))
    copy_cmd = f"cd {src_dir} && while IFS= read -r f || [ -n \"$f\" ]; do cp \"$f\" /tmp/artifacts/; done < /tmp/artifacts_list"
    ctr = ctr.with_exec(["/bin/sh", "-c", copy_cmd])
    await ctr.directory("/tmp/artifacts").export(output_dir)

    print(f"[+] Build complete. Artifacts exported to {output_dir}")
    for root, dirs, files in os.walk(output_dir):
        for file in files:
            print(f"  - {file}")
    return ctr

def use_remote_engine(remote: str):
    """Point the Dagger CLI at a remote engine instead of provisioning one locally.

    Accepts any runner host the Dagger CLI understands, e.g.
    tcp://host:1234, ssh://user@host, docker-container://name, kube-pod://name.
    """
    if remote:
        os.environ["_EXPERIMENTAL_DAGGER_RUNNER_HOST"] = remote
        print(f"[*] Building on remote Dagger engine: {remote}")

async def build_generic(shipyard_dir: str, image: str, patch_content: str = "", output_dir: str = "builds", interactive: bool = False, artifacts: str = "", version: str = "", remote: str = ""):
    """Build a 'generic' Shipfile: one that defines its own BaseImage/Setup/Build/Artifacts
    instead of relying on a distro's packaging tools (see Shipfile.Build)."""
    from shipyard.patches import Patches

    use_remote_engine(remote)

    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        ctr = None
        ctr_pre_build = None
        try:
            p = Patches(shipyard_dir, pull=True)
            info = p.infoObject
            if version:
                p._checkout(version)
            ver = p._ver or "src"
            source_name = f"{info.Name}-{ver}"
            source_dir = os.path.abspath(info.Directory)
            print(f"[*] Starting generic build for {info.Name} on {image}")

            # 1. Run the shipfile's CodePatches against the pristine checkout
            host_source = client.host().directory(source_dir, exclude=[".git/"])
            patched = run_shipfile(client, host_source, shipyard_dir, source_name, str(ver))

            # 2. Build container: base image + patched source
            workdir = f"/work/{source_name}"
            ctr = client.container().from_(image).with_directory(workdir, patched).with_workdir(workdir)

            # 3. Apply any extra raw patch passed on the CLI directly to the tree
            if patch_content.strip():
                ctr = (
                    ctr.with_new_file("/tmp/shipyard.patch", patch_content)
                    .with_exec(["/bin/sh", "-c", "patch -p1 < /tmp/shipyard.patch"])
                )

            # 4. Setup + Build scripts defined by the Shipfile
            if info.Setup:
                ctr = ctr.with_exec(["/bin/sh", "-c", info.Setup])
            if not info.Build:
                raise ValueError("Generic Shipfiles must define a 'Build' script")
            ctr_pre_build = ctr
            ctr = ctr.with_exec(["/bin/sh", "-c", info.Build])

            artifact_pattern = artifacts or info.Artifacts
            if not artifact_pattern:
                raise ValueError("Generic Shipfiles must define 'Artifacts' (a regex matched against build output), or pass --artifacts")
            src_dir = info.ArtifactDir or workdir

            os.makedirs(output_dir, exist_ok=True)
            print(f"[*] listing artifacts in {src_dir}...")
            files = await find_files(ctr, src_dir)
            ctr = await export_artifacts(ctr, files, src_dir, artifact_pattern, output_dir)

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

async def build_package(image: str, package: str, patch_content: str = "", output_dir: str = "builds", interactive: bool = False, artifacts: str = "", shipyard_dir: str = ".", version: str = "", remote: str = ""):
    use_remote_engine(remote)
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
            ctr = await export_artifacts(ctr, files, src_dir, artifact_pattern, output_dir)

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
