#!/usr/bin/env python3
"""
Primary CLI 
"""
import os
import sys
import fire
import anyio

from shipyard.patches import Patches
from shipyard.patch import PatchFile
from shipyard.generator import new_project
from shipyard.version import Version

class ShipyardCLI:
    def __init__(self, directory="."):
        """Maintain a group of patches against a source repository"""
        self.dir = directory
    
    async def _load(self) -> Patches:
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
                p = Patches(d)
                await p.prepare()
                return p
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
        async def _init():
            try:
                new_project(self.dir, url, name)
                await self._load()
            except Exception as e:
                print(e, file=sys.stderr)
                exit(127)
        anyio.run(_init)

    def jumpstart(self):
        """Download multiple URLs to create a properly tagged git repo."""
        pass

    def versions(self):
        """List the versions of the source code. Denoting which versions have patches"""
        async def _versions():
            p = await self._load()
            print(f"versions of {p.infoObject.Name}")
            for v in await p.source.versions():
                # Print tag aswell as version
                t = p.infoObject.version_to_tag(v)
                if t != v:
                    t = f"{v} - tags/{t}"
                if v in p.versions:
                    print(f"{t} - {len(p.versions[v])} patches")
                else:
                    print(t)
        anyio.run(_versions)

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
        async def _test():
            p = await self._load()
            patch = patchfile
            if patchfile not in p.code_patches:
                try:
                    patch = PatchFile.from_file(patchfile)
                except Exception as e:
                    print("[!] Could not load patchfile or CodePatch:", e, file=sys.stderr)
                    exit(127)
            try:
                await p.test_patch(patch)
            except Exception as e:
                print("[!] Checkout branch:", e, file=sys.stderr)
                exit(127)
        anyio.run(_test)
    
    def build_version(self, version):
        """Build a patch for a new version. This will use all existing patch files to try and create a version"""
        async def _build():
            p = await self._load()
            await p.patch_version(version)
        anyio.run(_build)

    def export(self, version):
        """Export the version into a single patchfile"""
        async def _export():
            p = await self._load()
            try:
                res, patches = await p.export(version)
                print(res)
            except Exception as e:
                print("[!]", e)
                quit(1)
        anyio.run(_export)
    
    def list_source_files(self):
        """List all files of the source that are not in the gitignore"""
        async def _list():
            p = await self._load()
            p.get_file_list()
            for f in p._files:
                print(f)
        anyio.run(_list)
    
    def apply_code_patches(self, patches=[]):
        """Apply all the code patches to the current source"""
        async def _apply():
            p = await self._load()
            p.get_file_list()
            try:
                code_funcs, _ =  await p.apply_code_patches()
                for cf in code_funcs:
                    print(f"[+] applied {cf.__name__}")
            except Exception as e:
                print("[!]", e)
        anyio.run(_apply)

    def checkout(self, version=""):
        """Checkout the given version of the source. If not version is given it will reset the
        source back to the original state of the current version"""
        async def _checkout():
            p = await self._load()
            await p.source.reset()
            if version:
                await p.source.checkout(Version(version))
        anyio.run(_checkout)

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

        p = None
        patch_content = ""
        
        if patch:
            if os.path.isfile(patch):
                if patch.endswith(".patch"):
                    try:
                        with open(patch, "r") as f:
                            patch_content = f.read()
                        print(f"[*] Loaded patch from {patch}")
                    except Exception as e:
                        print(f"[!] Failed to read patch file {patch}: {e}", file=sys.stderr)
                        exit(1)
                elif patch.endswith(".py"):
                    # Assume it points to a Shipfile.py
                    directory = os.path.dirname(os.path.abspath(patch))
                    print(f"[*] Loading Shipfile from {directory}")
                    try:
                        p = Patches(directory)
                    except Exception as e:
                        print(f"[!] Failed to load shipfile {patch}: {e}", file=sys.stderr)
                        exit(1)
                else:
                    print(f"[!] Unknown file type for patch argument: {patch}. Expected .patch or .py", file=sys.stderr)
                    exit(1)
            elif os.path.isdir(patch):
                print(f"[*] Loading Shipfile from directory {patch}")
                try:
                    p = Patches(patch)
                except Exception as e:
                    print(f"[!] Failed to load from directory {patch}: {e}", file=sys.stderr)
                    exit(1)
            else:
                 print(f"[!] Patch path not found: {patch}", file=sys.stderr)
                 exit(1)
        else:
            # Default behavior: try to find shipfile in current/parent dirs
            try:
                p = self._load()
            except Exception:
                # If we can't load a shipfile and no patch is provided, we can't proceed unless user provided a patch file
                pass

        # Determine package name
        if package:
            pkg_name = package
        elif p and getattr(p.infoObject, "Package", None):
            pkg_name = p.infoObject.Package
        else:
            print("[!] Error: Package name not specified. Please provide the 'package' argument or define 'Package' in your Shipfile.", file=sys.stderr)
            exit(1)

        # Resolve version if empty
        resolved_version = version

        async def _resolve_and_export():
            nonlocal resolved_version, patch_content, p

            if p:
                await p.prepare()
            else:
                # Default behavior: try to find shipfile in current/parent dirs
                try:
                    p = await self._load()
                except Exception:
                    # If we can't load a shipfile and no patch is provided, we can't proceed unless user provided a patch file
                    pass

            if not resolved_version and p:
                try:
                    vers = await p.source.versions()
                    if vers:
                        resolved_version = str(vers[-1])
                except Exception:
                    pass

            # Generate patch content if we haven't already (from Shipfile)
            if not patch_content:
                if p:
                    print(f"[*] Preparing patch for {pkg_name}...")
                    try:
                        patch_content, _ = await p.export(resolved_version)
                    except Exception as e:
                        print(f"[!] Failed to export patch: {e}", file=sys.stderr)
                        exit(1)

        anyio.run(_resolve_and_export)

        if not patch_content:
            # Should have been caught by "Package name not specified" or loading logic, but just in case
            print("[!] Error: No patch content available. Provide a .patch file or a valid Shipfile.", file=sys.stderr)
            exit(1)
        
        # Determine output directory
        if output:
            image_parts = image.split(":")
            if len(image_parts) > 1:
                output_dir = os.path.join(output, *image_parts)
            else:
                output_dir = os.path.join(output, image)
        else:
            output_dir = "build-output"

        print(f"[*] Starting build for {pkg_name} on {image}...")
        async def _build_package():
            await build_package(
                image or "",
                pkg_name or "",
                patch_content or "",
                output_dir or "",
                bool(interactive),
                artifacts or "",
                os.path.abspath(p._dir) if p else ".",
                resolved_version or ""
            )
        anyio.run(_build_package)


def run():
    #p = Patch.from_file(sys.argv[1])
    #print(p.dump())
    fire.Fire(ShipyardCLI)

if __name__ == "__main__":
    run()
