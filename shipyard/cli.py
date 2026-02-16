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
    
    def _load(self) -> Patches:
        # @TODO: Search any python files in the current dir for valid Shipfile objects
        # TODO: Search any python files in the current dir
        path = os.path.join(self.dir, "shipfile.py")
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
                return Patches(d)
            #except Exception as e:
                #print(f"[!] Detected a shipyard.py file. But there were errors loading it: {e}", file=sys.stderr)
                #exit(127)
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
            p = self._load()
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
        if not patchfile in p.code_patches:
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
        p.get_file_list()
        for f in p._files:
            print(f)
    
    def apply_code_patches(self, patches=[]):
        """Apply all the code patches to the current source"""
        p = self._load()
        p.get_file_list()
        try:
            code_funcs, _ =  p.apply_code_patches()
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

    def build(self, image, package, version=""):
        """Build a package using Dagger orchestration.
        
        image: The base image to build on (e.g. debian:bookworm, rockylinux:9)
        package: The name of the package to build
        version: The version of the source code to use (optional, defaults to current/latest)
        """
        try:
            import anyio
            from shipyard.engines.dagger import build_package
        except ImportError as e:
            print(f"[!] Dagger SDK or dependencies not found: {e}", file=sys.stderr)
            print("    Please install with: pip install dagger-io anyio", file=sys.stderr)
            exit(1)

        p = self._load()
        
        # If version is not specified, try to guess or use current
        # But export() takes a version. If empty, it uses first available?
        # Let's check p.export().
        # It takes version="". If empty, uses p.source.versions()[0] (latest/first).
        
        print(f"[*] Preparing patch for {package}...")
        try:
            patch_content, _ = p.export(version)
        except Exception as e:
            print(f"[!] Failed to export patch: {e}", file=sys.stderr)
            exit(1)
        
        print(f"[*] Starting build on {image}...")
        anyio.run(build_package, image, package, patch_content)


def run():
    #p = Patch.from_file(sys.argv[1])
    #print(p.dump())
    fire.Fire(ShipyardCLI)

if __name__ == "__main__":
    run()
