from .base import DistroDriver
import dagger
import re
from io import StringIO

class RPMDriver(DistroDriver):
    def setup(self, container: dagger.Container) -> dagger.Container:
        # Rocky Linux logic
        if "rockylinux:8" in self.image:
            container = (
                container
                .with_exec(["echo", "rocky:8: Enabling Powertools"])
                .with_exec(["dnf", "install", "-y", "epel-release", "dnf-plugins-core"])
                .with_exec(["dnf", "config-manager", "--set-enabled", "powertools"])
                .with_exec(["dnf", "update", "-y"])
            )
        elif "rockylinux:9" in self.image:
             container = (
                container
                .with_exec(["echo", "rocky:9: Enabling CRB"])
                .with_exec(["dnf", "install", "-y", "epel-release", "dnf-plugins-core"])
                .with_exec(["dnf", "config-manager", "--set-enabled", "crb"])
                .with_exec(["dnf", "update", "-y"])
            )
        elif "centos:" in self.image:
            # Centos logic (sed mirrors)
            container = (
                container
                .with_exec(["sed", "-i", "s/mirrorlist/#mirrorlist/g", "/etc/yum.repos.d/CentOS-*"])
                .with_exec(["sed", "-i", "s|#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g", "/etc/yum.repos.d/CentOS-*"])
                .with_exec(["yum", "update", "-y"])
            )
        else:
             # Generic RPM update (Fedora, RHEL, AmazonLinux)
             container = container.with_exec(["yum", "update", "-y"])

        return (
            container
            .with_exec([
                "yum", "install", "-y",
                "gcc", "rpmdevtools", "yum-utils", "make",
                "nc", "vim", "python3", "python3-pip", "git"
            ])
        )

    def install_build_deps(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_workdir("/tmp/build")
            .with_exec(["yum-builddep", "--skip-broken", "-y", package])
            # Setup mockbuild user
            .with_exec(["useradd", "-m", "mockbuild"])
            .with_exec(["groupadd", "-f", "mock"]) # -f to avoid error if exists
            .with_exec(["usermod", "-G", "wheel", "mockbuild"])
            .with_exec(["/bin/sh", "-c", 'echo "%wheel  ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers'])
            .with_exec(["/bin/sh", "-c", 'echo "root:toor" | chpasswd'])
            .with_exec(["rpmdev-setuptree"])
            .with_exec(["/bin/sh", "-c", "echo '%_topdir      /tmp/build' > ~/.rpmmacros"])
            .with_exec(["yumdownloader", "--source", package])
            .with_exec(["/bin/sh", "-c", "rpm -ivh *.src.rpm"])
            .with_exec(["/bin/sh", "-c", "rpmbuild -bp SPECS/*.spec"])
        )

    def prepare_source(self, container: dagger.Container, package: str) -> dagger.Container:
        # RPM source preparation is done in install_build_deps (rpmbuild -bp)
        return container
    
    async def apply_patch(self, container: dagger.Container, patch_content: str, package: str) -> dagger.Container:
         # Need to find SPEC file
         # We need to glob it?
         # `await container.directory("/tmp/build/SPECS").entries()`?
         # Then read the first one.
         
         specs = await container.directory("/tmp/build/SPECS").entries()
         if not specs:
             raise ValueError("No SPEC file found in /tmp/build/SPECS")
         spec_file = specs[0]
         # We assume the first one is correct if multiple (unlikely for single package build)
         if not spec_file.endswith(".spec"):
             # Filter for spec
             for s in specs:
                 if s.endswith(".spec"):
                     spec_file = s
                     break
         
         spec_path = f"/tmp/build/SPECS/{spec_file}"
         
         content = await container.file(spec_path).contents()
         
         # Python logic to patch spec
         new_content = self.patch_spec_file(content, package)
         
         # Write back
         return (
             container
             .with_new_file(spec_path, new_content)
             .with_new_file(f"/tmp/build/SOURCES/{package}.patch", patch_content)
         )

    def patch_spec_file(self, content: str, package: str) -> str:
        # Implement logic from shipyard-build
        last = -1
        spec_format = ""
        lines = content.splitlines()
        for i in lines:
            if i.startswith("Patch"):
                try:
                    p_part = i.split(":")[0]
                    p_num = p_part[len("Patch"):]
                    if p_num.isnumeric():
                        last = max(int(p_num), last)
                except:
                    pass
            if i.strip().startswith("%patch"):
                spec_format = i.strip()
        
        # Helper to reinsert
        def reinsert(text, regex, lines):
            res = re.search(regex, text)
            if not res:
                return text
            idx = res.end()
            return text[:idx] + "\n" + lines + text[idx:]

        # Register the patch file
        if last >= 0: # last could be 0 if Patch0 exists
            new = f"Patch{last+1}: {package}.patch"
            # We look for the line defining Patch{last}
            # Regex: `Patch{last}:.+` matches the line content
            content = reinsert(content, re.compile(f"Patch{last}:.+"), new)
            
            # Tell prep to apply the patch
            patch_cmd = ""
            if '-P ' in spec_format:
                # New spec format '%patch -P 111 -p1'
                patch_cmd = f"%patch -P {last+1} -p1"
            else:
                # Legacy spec format '%patch111 -p1'
                patch_cmd = f"%patch{last+1} -p1"
            
            # We look for the line applying Patch{last}
            # Regex: `%patch.*{last}.*`
            content = reinsert(content, re.compile(f"%patch.*{last}.*"), patch_cmd)
        else:
             # If no patches exist, we should probably add Patch0 after Source0?
             # And %patch0 after %setup?
             # This is complex without knowing spec structure well.
             # For now, we assume patches exist as per shipyard-build logic
             # @TODO: Implement fallback to insert Patch0 if no patches exist in the spec file
             print(f"Warning: No existing patches found in SPEC file for {package}. Patch application might fail.")
             
        return content

    def build(self, container: dagger.Container, package: str) -> dagger.Container:
        return (
            container
            .with_workdir("/tmp/build")
            # We assume spec file is in SPECS/
            .with_exec(["/bin/sh", "-c", "rpmbuild --define 'debug_package %{nil}' --nocheck -bb SPECS/*.spec"])
        )

    def get_artifact_pattern(self, package: str) -> str:
        return r".*\.rpm$"

    def get_artifact_dir(self) -> str:
        return "/tmp/build/RPMS"