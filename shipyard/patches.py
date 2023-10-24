import sys
import inspect
import glob

from os import path, walk, makedirs

from shipyard.patch import Patch
from shipyard.utils import _load_object, getClosestVersions
from shipyard.git import SourceProgram, SourceManager, GitMgr

class Patches:
    """A manager for the patches that loops through a directory to figure out
    each version supported and that patches that we have for each one"""
    def __init__(self, directory, source_directory=""):
        self._dir = directory
        self.patches = {}
        self.versions = {}
        self.infoObject = None
        self.load()
        # If we wanted, we could use a different source manager here
        self.source:SourceManager = GitMgr(self.infoObject, destination=source_directory)

    def _patch_dir(self, *others):
        return path.join(*[i for i in (self._dir, self.infoObject.Patches, *others) if i and i != "."])

    def load(self):
        # Load the object class
        for f in glob.glob(path.join(self._dir, "*.py")):
            obj = _load_object(f)
            if obj is not None:
                self.infoObject = SourceProgram.from_object(obj)
                break
        
        for root, dirs, files in walk(self._dir):
            if dirs:
                continue
            _, version = path.split(root)
            self.versions[version] = set()
            for p in files:
                if not p.endswith(".diff") and not p.endswith(".patch"):
                    continue
                patch = Patch.from_file(path.join(root, p))
                self.patches[patch.Name] = patch
                self.versions[version].add(patch)
            if not self.versions[version]:
                del self.versions[version]
    
    def apply_similar_patch(self, patch, similarversions, outdir):
        """Given patch: find other versions of the same patch and try to apply them"""
        for v in similarversions:
            # Get the patch that has the same name for the version
            p = [p for p in self.versions[v] if p == patch]
            if not p:
                continue
            p = p[0]
            try:
                self.source.apply(p)
                contents = self.source.refresh(p)
                _, fname = path.split(p.Filename)
                out = self._patch_dir(v, fname)
                with open(path.join(out), "w") as f:
                    f.write(contents)
                print(f"[+] Applied {p.Name} from {v}")
                return True
            except Exception as E:
                print(E)
                continue
        # Could not find a similar patch
        return False

    def patch_version(self, version):
        """Patch a version with all the patches from a previous version"""
        closestVers = getClosestVersions(version, list(self.versions.keys()))
        patches = self.versions.get(closestVers[0])
        print(f"[*] Attempting to patch {version} with {len(patches)} patches from version {closestVers[0]}")
        self.source.checkout(version)
        self.source.reset()
        outdir = self._patch_dir(version)
        makedirs(outdir, exist_ok=True)
        for p in patches:
            try:
                self.source.apply(p)
                contents = self.source.refresh(p)
                _, fname = path.split(p.Filename)
                with open(path.join(outdir, fname), "w") as f:
                    f.write(contents)
            except Exception as e:
                print(e)
                self.source.reset()
                # Fall back to trying every similar patch
                if not self.apply_similar_patch(p, closestVers, outdir):
                    print("[!] Failed to find a valid patch for", p.Name)
        print("[+] Saved patches to", outdir)
        self.source.reset()

    def test_patch(self, patch: Patch):
        """Test a patchfile on all versions of a source code"""
        for v in self.source.versions():
            self.source.checkout(v)
            if self.source.apply(patch, check=True):
                print("[+] PASS", v)
            else:
                print("[!] FAIL", v)
    
    def validate_version(self, version: str):
        """Validate that a version is compatible with all the patches we have for it"""
        if version not in self.versions:
            raise ValueError("No patches found for version " + version)
        self.source.checkout(version)
        try:
            for p in self.versions[version]:
                self.source.apply(p)
            print("[+] All patches successfully applied")
        finally:
            self.source.reset()


    def dump(self):
        print("patches versions:", list(self.versions.keys()))
        print("\npatches:", list(self.patches.keys()))
        print("\nmissing version patches:", [v for v in self.source.versions() if v not in self.versions])
