import sys
import dagger
import os
import re
from shipyard.drivers.debian import DebianDriver
from shipyard.drivers.rpm import RPMDriver
from shipyard.drivers.arch import ArchDriver

async def build_package(image: str, package: str, patch_content: str, output_dir: str = "builds", interactive: bool = False, artifacts: str = ""):
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
