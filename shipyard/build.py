#!/usr/bin/env python3

"""
Use Shipyard to build a package on a host.

Expects to be in a build directory with all the dependancies already installed
"""

import sys
import os
import re
import subprocess
import fire
import glob

from dataclasses import dataclass
from shipyard.patches import Patches

@dataclass
class Pkg:
    OriginalName: str
    Name: str
    Version: str
    AdditionalVersion: str
    Folder: str

default = {
    "capture_output": True,
    "encoding": "utf-8",
}

def run(folder, args, err="", **kwargs):
    res = subprocess.run(
        args,
        capture_output=True,
        cwd=folder,
        encoding="utf-8",
        **kwargs
    )
    if res.returncode != 0 and err:
        raise ValueError(f"{err}: {res.stdout}\n{res.stderr}")
    return res

def GeneratePatch(shipcontext, pkg: Pkg) -> str:
    """Generate a patch using shipyard
    shipcontext can either be file, dir, or zipfile

    if its a dir, check for a shipfile in it
    if its a zip, unzip it and use that dir as the new shipcontext
    if its a file, thats the shipfile
    """
    if os.path.isdir(shipcontext):
        print("build was given a directory. contents:", os.listdir(shipcontext))
        p = Patches(shipcontext)
    elif os.path.isfile(shipcontext):
        #TODO: Check if its a zip file here or maybe have shipyard handle that part?
        print("build was given a file: " + shipcontext)
        pth, _ = os.path.split(shipcontext)
        p = Patches(pth)

    p.patch_version(pkg.Version)
    patch, _ = p.export(pkg.Version)
    return patch



def rpm(args):
    raise NotImplementedError("RPM functionality is not implemented")


# TODO: SUPPORTED_MODES = ["arch", "deb", "rpm"]
SUPPORTED_MODES = ["deb"]

class cli:
    def __init__(self, mode=""):
        self.mode = os.environ.get("BUILD_MODE", mode)
        if not self.mode:
            raise ValueError(f"Build mode is not specified. Set BUILD_MODE or pass --mode. Supported modes: {SUPPORTED_MODES}")
    
    def _get_pkg(self, package) -> Pkg:
        """Return a Pkg object with version information. System dependant"""
        pkg = Pkg(package, package, "", "", "")
        # Get the current version
        if self.mode == "deb":
            # There should be a dsc file laying around here that we can parse the package version from
            f = glob.glob("*.dsc")
            if not f:
                print("[!] No APT source DSC file laying around!. Exiting")
                exit(127)
            reg = r"(.+)_([\d\w\.]+)(-(.+))?\.dsc"
            groups = re.compile(reg).match(f[0])
            if not groups:
                print(f"[!] Filename passed was not valid. Needs to match regex r'{reg}'")
                exit(1)
            if not package:
                package = groups.group(1)
            pkg = Pkg(package, groups.group(1), groups.group(2), groups.group(4), "")
            pkg.Folder = f"{pkg.Name}-{pkg.Version}"
            print(f"[*] found '{pkg.Name}-{pkg.Version}' from file '{f[0]}'")
        else:
            print("[!] mode not supported for gen", self.mode)
            exit(127)
        
        if not os.path.isdir(pkg.Folder):
            print(f"[!] Source folder '{pkg.Folder}' not found. Was the source package installed properly?")
            exit(1)
        return pkg

    def gen(self, shipcontext, patchfile, package=""):
        """Generate a patch for the current app version using the given shipfile"""
        pkg = self._get_pkg(package)
        # Use shipyard to generate a patchfile now for this version
        patch = GeneratePatch(shipcontext, pkg)
        if not patch:
            raise ValueError("Could not export patch!")
        with open(patchfile, "w") as f:
            f.write(patch)
        print("[+] Patch saved to " + patchfile)

    def apply(self, patchfile, package=""):
        """Apply a patchfile to the package"""
        pkg = self._get_pkg(package)
        print(f"[*] applying patchfile {patchfile} to {pkg.Name}-{pkg.Version}")
        
        if self.mode == "deb":
            res = run(pkg.Folder, ["quilt", "import", patchfile], "Error importing quilt patch")
            print(res.stdout)
            res = run(pkg.Folder, ["quilt", "push"], "Error pushing quilt patch")
            print(res.stdout)
            res = run(pkg.Folder, ["quilt", "refresh"], "Error refreshing quilt patch")
            print(res.stdout)
            return
        raise NotImplementedError(f"Cannot apply on '{self.mode}' systems")
    
    def build(self, package=""):
        """Build the source after applying patches"""
        pkg = self._get_pkg(package)
        if self.mode == "deb":
            env = os.environ.copy()
            env["DEB_BUILD_OPTIONS"] = "notest nocheck"
            env["DEBUILD_DPKG_BUILDPACKAGE_OPTS"] = "-d"
            res = subprocess.run(
                ["debuild", "--no-lintian", "-d", "-uc", "-us", "-b"],
                stdout=sys.stdout,
                stderr=sys.stderr,
                cwd=pkg.Folder,
                encoding="utf-8",
                env=env
            )
            exit(res.returncode)
        raise NotImplementedError(f"Cannot apply on '{self.mode}' systems")

if __name__ == '__main__':
    fire.Fire(cli)