"""
Runs a Shipfile's CodePatches against a source directory, in place.

This is the only part of the pipeline that executes inside the shipyard
container:

    python -m shipyard.runner <shipfile_dir> <source_dir> [version]
"""
import os
import sys

from shipyard.dirmgr import DirMgr
from shipyard.patches import Patches
from shipyard.version import Version

def run(shipfile_dir: str, source_dir: str, version: str = "") -> list:
    """Apply every CodePatch of the shipfile to source_dir. Returns the names of the patches that ran"""
    if not version:
        # Distro source trees are unpacked as <package>-<version>
        version = os.path.basename(os.path.abspath(source_dir))

    p = Patches(shipfile_dir, pull=False)
    p._ver = Version(version)
    p.source = DirMgr(source_dir, version, p.infoObject.Variables)
    ran, files = p.patch()
    names = sorted(getattr(r, "__name__", str(r)) for r in ran)
    for name in names:
        print(f"[+] applied {name}")
    return names

def main(argv=None) -> int:
    argv = sys.argv[1:] if argv is None else argv
    if len(argv) not in (2, 3):
        print("usage: python -m shipyard.runner <shipfile_dir> <source_dir> [version]", file=sys.stderr)
        return 2
    try:
        run(*argv)
    except Exception as e:
        print(f"[!] {e}", file=sys.stderr)
        return 1
    return 0

if __name__ == "__main__":
    sys.exit(main())
