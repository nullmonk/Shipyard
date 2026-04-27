import sys
import dagger
import os
import re
import anyio
from shipyard.drivers.debian import DebianDriver
from shipyard.drivers.rpm import RPMDriver
from shipyard.drivers.arch import ArchDriver
from shipyard.patches import Patches
from shipyard.dagger_mgr import DaggerMgr
from shipyard.version import Version

async def build_package(image: str, package: str = "", patch: str = "", output_dir: str = "builds", interactive: bool = False, artifacts: str = "", version: str = ""):
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # Determine driver
        if any(x in image for x in ["debian", "ubuntu", "linuxmint", "kali"]):
            driver = DebianDriver(image)
        elif any(x in image for x in ["redhat", "centos", "rocky", "fedora", "amazonlinux"]):
            driver = RPMDriver(image)
        elif "archlinux" in image:
             driver = ArchDriver(image)
        else:
            raise ValueError(f"Unsupported image: {image}")

        # If package name is not provided, we will try to get it from the shipfile later
        pkg_name = package
        print(f"[*] Starting build for {pkg_name or 'unspecified package'} on {image}")

        ctr = None
        ctr_pre_build = None
        try:
            ctr = client.container().from_(image)
            ctr = driver.setup(ctr)

            # If we don't have a package name yet, we might need to load the shipfile first
            # But driver.install_build_deps needs a package name.
            # So if we have a shipfile, let's load it now to get the package name.
            p = None
            if patch:
                if os.path.isdir(patch) or (os.path.isfile(patch) and patch.endswith(".py")):
                    shipyard_dir = patch if os.path.isdir(patch) else os.path.dirname(os.path.abspath(patch))
                    p = Patches(shipyard_dir, pull=False) # Don't pull on host
                    if not pkg_name and hasattr(p.infoObject, "Package"):
                        pkg_name = p.infoObject.Package
                        print(f"[*] Resolved package name to {pkg_name}")

            if not pkg_name:
                raise ValueError("Package name must be specified or defined in a Shipfile")

            ctr = driver.install_build_deps(ctr, pkg_name)
            ctr = driver.prepare_source(ctr, pkg_name)

            source_dir = await driver.get_source_dir(ctr, pkg_name)

            # Set SHIPYARD_SOURCE env in container
            ctr = ctr.with_env_variable("SHIPYARD_SOURCE", source_dir)

            patch_content = ""
            if patch:
                if os.path.isfile(patch) and patch.endswith(".patch"):
                    with open(patch, "r") as f:
                        patch_content = f.read()
                elif p:
                    # It's a shipfile/directory
                    print(f"[*] Generating patch from Shipfile for {pkg_name}...")

                    def _generate_patch_content(p, ctr, source_dir, version, client):
                        p.source = DaggerMgr(ctr, path=source_dir, client=client)

                        resolved_version = version
                        if not resolved_version:
                            # Try to get version from the container's source
                            # DaggerMgr currently just returns "latest"
                            pass

                        # Apply patches
                        relevant_patches = []
                        if resolved_version:
                            relevant_patches = p.versions.get(Version(resolved_version), [])

                        p.patch(patches=relevant_patches)

                        # Generate unified patch
                        content = p.source.refresh()

                        # Variable substitution
                        for k, v in p.infoObject.Variables.items():
                            content = content.replace(k, v)

                        return content

                    patch_content = await anyio.to_thread.run_sync(_generate_patch_content, p, ctr, source_dir, version, client)

            if patch_content:
                print(f"[*] Applying generated/provided patch to {pkg_name}...")
                ctr = await driver.apply_patch(ctr, patch_content, pkg_name)

            ctr_pre_build = ctr
            ctr = driver.build(ctr, pkg_name)
            os.makedirs(output_dir, exist_ok=True)
            artifact_pattern = artifacts if artifacts else driver.get_artifact_pattern(pkg_name)
            src_dir = driver.get_artifact_dir()
            print(f"[*] listing artifacts in {src_dir}...")
            files = await driver.list_artifacts(ctr)
            matches = [f for f in files if re.search(artifact_pattern, f)]
            if not matches:
                print(f"[!] Warning: No artifacts found matching '{artifact_pattern}'")
            else:
                print(f"[*] Found {len(matches)} artifacts matching '{artifact_pattern}'")
                # We can copy artifacts to a clean directory inside container and export that.
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
                # Use the most recent valid container state
                target_ctr = ctr_pre_build or ctr
                if target_ctr:
                    print("[*] Dropping into interactive shell...")
                    await target_ctr.terminal().sync()
                else:
                    print("[!] No container available to drop into.")
            else:
                raise e
