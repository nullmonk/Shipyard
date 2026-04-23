import sys
import dagger
import os
import re
import anyio
from shipyard.drivers.debian import DebianDriver
from shipyard.drivers.rpm import RPMDriver
from shipyard.drivers.arch import ArchDriver
from shipyard.patches import Patches
from shipyard.dagger import DaggerMgr
from shipyard.version import Version

async def build_package(image: str, package: str, patch_content: str, output_dir: str = "builds", interactive: bool = False, artifacts: str = "", shipyard_dir: str = ".", version: str = ""):
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

        print(f"[*] Starting build for {package} on {image}")

        ctr = None
        ctr_pre_build = None
        try:
            ctr = client.container().from_(image)
            ctr = driver.setup(ctr)
            ctr = driver.install_build_deps(ctr, package)
            ctr = driver.prepare_source(ctr, package)

            source_dir = await driver.get_source_dir(ctr, package)

            # Set SHIPYARD_SOURCE env in container
            ctr = ctr.with_env_variable("SHIPYARD_SOURCE", source_dir)

            # Use Patches in a separate thread because it's synchronous but DaggerMgr uses anyio.from_thread.run
            def _patch_source(shipyard_dir, ctr, version, source_dir):
                p = Patches(shipyard_dir)
                p.source = DaggerMgr(ctr, path=source_dir)

                # Resolve version if needed
                resolved_version = version
                if not resolved_version:
                    vers = p.source.versions()
                    if vers:
                        resolved_version = str(vers[-1])

                if resolved_version:
                    print(f"[*] Resolved version to {resolved_version}")
                    p._checkout(resolved_version)

                # Apply patches (both file-based and code-based)
                print(f"[*] Applying patches to {package} source in container...")
                relevant_patches = p.versions.get(Version(resolved_version), []) if resolved_version else []
                p.patch(patches=relevant_patches)
                return p.source.container

            ctr = await anyio.to_thread.run_sync(_patch_source, shipyard_dir, ctr, version, source_dir)

            # If patch_content was provided from CLI (and it's not empty), apply it using the driver
            # This allows user-provided .patch files to work alongside Shipfile patches
            if patch_content:
                print(f"[*] Applying custom patch content...")
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
