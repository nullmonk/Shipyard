#!/usr/bin/env python3
"""
Primary CLI 
"""
import os
import sys
import fire

from shipyard.patches import Patches
from shipyard.patch import PatchFile
from shipyard.generator import new_project
from shipyard.version import Version

class ShipyardCLI:
    def __init__(self, directory="."):
        """Maintain a group of patches against a source repository"""
        self.dir = directory
    
    def _load(self, pull=True) -> Patches:
        # @TODO: Search any python files in the current dir for valid Shipfile objects
        paths = [
            os.path.join(self.dir, "shipfile.py"),
            os.path.join(".", "shipfile.py"),
            os.path.join("..", "shipfile.py"),
            os.path.join("../..", "shipfile.py")
        ]
        for path in paths:
            if os.path.exists(path):
                # Detected a shipyard file in the current dir, assume this is the patch folder
                d, _ = os.path.split(path)
                return Patches(d, pull=pull)
        print("[!] This directory does not appear to be a Shipyard directory. Try passing a directory with '--directory' or setting it up in the current directory", file=sys.stderr)
        print(f"{sys.argv[0]} init <url>", file=sys.stderr)
        exit(127)

    def init(self, url, name=""):
        """Initialize a new Shipyard project directory for the given url

        url: URL to a git repository with the source code
        name: override the name of the project (default repository name)
        """
        try:
            new_project(self.dir, url, name)
            self._load()
        except Exception as e:
            print(e, file=sys.stderr)
            exit(127)

    def jumpstart(self):
        """Download multiple URLs to create a properly tagged git repo."""
        pass

    def versions(self):
        """List the versions of the source code. Denoting which versions have patches"""
        p = self._load()
        print(f"versions of {p.infoObject.Name}")
        for v in p.source.versions():
            # Print tag aswell as version
            t = p.infoObject.version_to_tag(v)
            if t != v:
                t = f"{v} - tags/{t}"
            if v in p.versions:
                print(f"{t} - {len(p.versions[v])} patches")
            else:
                print(t)

    def import_patch(self, name, description=""):
        """import a new patchfile
        
        This patch can later be applied to multiple versions of the source
        
        Example: git diff file.c | shipyard import mypatch "this is an awesome patchfile" """
        # We don't actually use _load here, so it should be fine.
        # But for consistency and to avoid future issues:
        buf = sys.stdin.read()
        buf = buf.strip()
        if not buf:
            print("[*] Empty contents. Exiting")
            return
        

        # Import it into scratch by default
        _, ext = os.path.splitext(name)
        if not ext:
            name += ".patch"
        pth = os.path.join(self.dir, "scratch")
        os.makedirs(pth, exist_ok=True)
        pth = os.path.join(pth, name)
        p = PatchFile(filename=pth, contents=buf)
        if description:
            p.Description = description
        with open(pth, "w") as of:
            of.write(p.dump())
        print("[+] Saved new patch file to", pth)
    
    def test_patch(self, patchfile):
        """Test a patch file against all versions"""
        p = self._load()
        patch = patchfile
        if patchfile not in p.code_patches:
            try:
                patch = PatchFile.from_file(patchfile)
            except Exception as e:
                print("[!] Could not load patchfile or CodePatch:", e, file=sys.stderr)
                exit(127)
        try:
            p.test_patch(patch)
        except Exception as e:
            print("[!] Checkout branch:", e, file=sys.stderr)
            exit(127)
    
    def build_version(self, version):
        """Build a patch for a new version. This will use all existing patch files to try and create a version"""
        p = self._load()
        p.patch_version(version)

    def export(self, version):
        """Export the version into a single patchfile"""
        p = self._load()
        try:
            res, patches = p.export(version)
        except Exception as e:
            print("[!]", e)
            quit(1)
        print(res)
    
    def list_source_files(self):
        """List all files of the source that are not in the gitignore"""
        p = self._load()
        for f in p.source.list_files():
            print(f)
    
    def apply_code_patches(self, patches=[]):
        """Apply all the code patches to the current source"""
        p = self._load()
        p._files = p.source.list_files()
        try:
            code_funcs, _ = p.apply_code_patches(patches)
            for cf in code_funcs:
                print(f"[+] applied {cf.__name__}")
        except Exception as e:
            print("[!]", e)

    def checkout(self, version=""):
        """Checkout the given version of the source. If not version is given it will reset the
        source back to the original state of the current version"""
        p = self._load()
        p.source.reset()
        if version:
            p.source.checkout(Version(version))

    def build(self, image=None, package=None, version="", patch=None, interactive=False, artifacts=None, output=None, i=False, p=None):
        """Build a package using Dagger orchestration.
        
        image: The base image to build on (e.g. debian:bookworm, rockylinux:9)
        package: The name of the package to build. Optional if defined in Shipfile.
        version: The version of the source code to use (optional, defaults to current/latest)
        patch: Optional path to a .patch file, a Shipfile.py, or a directory containing a Shipfile.py
        interactive: Drop into an interactive shell on failure or completion
        artifacts: Glob pattern to filter exported artifacts (e.g. "openssh*.rpm")
        output: Directory to save artifacts. If set, artifacts are saved in <output>/<image>/
        """
        # Handle short flags
        if i:
            interactive = True
        if p and not patch:
            patch = p

        if image is None or image.startswith("-"):
            print("[!] Error: Image name is required and cannot start with a hyphen.", file=sys.stderr)
            print("    Usage: shipyard build <image> [package] [--patch <path>] [--interactive]", file=sys.stderr)
            exit(1)

        try:
            import anyio
            from shipyard.engines.dagger import build_package
        except ImportError as e:
            print(f"[!] Dagger SDK or dependencies not found: {e}", file=sys.stderr)
            print("    Please install with: pip install dagger-io anyio", file=sys.stderr)
            exit(1)

        shipfile_dir = None
        patch_content = ""

        if patch:
            if os.path.isfile(patch) and patch.endswith(".patch"):
                # An extra raw patch, applied on top of whatever the shipfile does
                with open(patch, "r") as f:
                    patch_content = f.read()
                print(f"[*] Loaded patch from {patch}")
            elif os.path.isfile(patch) and patch.endswith(".py"):
                shipfile_dir = os.path.dirname(os.path.abspath(patch))
            elif os.path.isdir(patch):
                shipfile_dir = os.path.abspath(patch)
            else:
                print(f"[!] Patch path not found or not a .patch, .py or directory: {patch}", file=sys.stderr)
                exit(1)

        if shipfile_dir is None and (patch_content == "" or not package):
            shipfile_dir = os.path.abspath(self._load(pull=False)._dir)

        # Determine package name
        pkg_name = package
        if not pkg_name and shipfile_dir:
            pkg_name = getattr(Patches(shipfile_dir, pull=False).infoObject, "Package", None)
        if not pkg_name:
            print("[!] Error: Package name not specified. Please provide the 'package' argument or define 'Package' in your Shipfile.", file=sys.stderr)
            exit(1)

        # Determine output directory
        if output:
            output_dir = os.path.join(output, *image.split(":"))
        else:
            output_dir = "build-output"

        print(f"[*] Starting build for {pkg_name} on {image}...")
        anyio.run(
            build_package,
            image,
            pkg_name,
            patch_content,
            output_dir,
            bool(interactive),
            artifacts or "",
            shipfile_dir or "",
            version or "",
        )


def run():
    fire.Fire(ShipyardCLI)

if __name__ == "__main__":
    run()
