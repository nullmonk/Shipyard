import sys
import inspect
import glob
import re
import fnmatch

from os import path, walk, makedirs
from collections import defaultdict

from shipyard.patch import PatchFile
from shipyard.utils import _load_object, getClosestVersions
from shipyard.git import SourceProgram, SourceManager, GitMgr

class Patches:
    """A manager for the patches that loops through a directory to figure out
    each version supported and that patches that we have for each one"""
    def __init__(self, directory, source_directory=""):
        self._dir = directory
        self.patches = {}
        self.code_patches = {} # Patches that are functions and not .patch files
        self.code_res = defaultdict(list) # when a file matches an RE in this array, goto the func it points to
        self.versions = {}
        self.infoObject = None

        self._files = [] # List of all files in the source
        self.load()
        # If we wanted, we could use a different source manager here
        self.source:SourceManager = GitMgr(self.infoObject, destination=source_directory)

    def _patch_dir(self, *others):
        """Where patches reside"""
        return path.join(*[i for i in (self._dir, self.infoObject.Patches, *others) if i and i != "."])

    def _load_code_patch(self, cp):
        """Load the regexs for a single CodePatch"""
        self.code_patches[cp.__name__] = cp
        for reg in getattr(cp, "__patch_files", []):
            r = re.compile(reg)
            self.code_res[r].append(cp)
    
    def load(self):
        """Load and parse the shipfile as well as the patches"""
        # Load the object class
        for f in glob.glob(path.join(self._dir, "*.py")):
            obj = _load_object(f)
            if obj is not None:
                self.infoObject = SourceProgram.from_object(obj)
                # Load the code patches from the obj
                for _, func in inspect.getmembers(obj, predicate=inspect.isfunction):
                    if getattr(func, "__patch_files", False):
                        self._load_code_patch(func)
                break
        
        for root, dirs, files in walk(self._dir):
            if dirs:
                continue
            _, version = path.split(root)
            self.versions[version] = set()
            for p in files:
                if not p.endswith(".diff") and not p.endswith(".patch"):
                    continue
                patch = PatchFile.from_file(path.join(root, p))
                self.patches[patch.Name] = patch
                self.versions[version].add(patch)
            if not self.versions[version]:
                del self.versions[version]
    
    def get_file_list(self) -> list[str]:
        """List files, honoring the .gitignore
        
        https://stackoverflow.com/questions/70745060/how-to-list-directory-files-excluding-files-in-gitignore
        https://stackoverflow.com/a/19859907
        """
        ignored = [".git"]
        if path.isfile(".gitignore"):
            with open(".gitignore") as f:
                ignored += [line for line in f.read().splitlines() if line]
        for root, dirs, files in walk(self.infoObject.resolve_source_directory()):
            if any(fnmatch.fnmatch(root, i) for i in ignored):
                dirs[:] = []
                continue
            _, d = path.split(root)
            if d in ignored or any(fnmatch.fnmatch(d, i) for i in ignored):
                dirs[:] = []
                continue
            for f in files:
                if f in ignored:
                    continue
                f = path.join(root, f)
                ign = any(fnmatch.fnmatch(f, i) for i in ignored)
                if not ign:
                    self._files.append(f)
        return self._files

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

    def test_patch(self, patch: PatchFile):
        """Test a patchfile on all versions of a source code"""
        is_code_patch = not isinstance(patch, PatchFile)
        if is_code_patch:
            if patch not in self.code_patches:
                raise FileNotFoundError(f"code patch '{patch}' not found")
        
        versions = self.source.versions()
        for v in versions:
            if len(versions) > 1:
                self.source.reset()
                self.source.checkout(v)
            if not is_code_patch:
                if self.source.apply(patch, check=True):
                    print("[+] PASS", v)
                else:
                    print("[!] FAIL", v)
            else:
                self.get_file_list()
                try:
                    for f in self._files:
                        self.__run_patches_on_file(f, patches=[patch])
                    print("[+] PASS", v)
                except Exception as e:
                    print(f"[!] FAIL {v}: {e}")
    
    def apply_code_patches(self, patches=[]):
        for f in self._files:
            self.__run_patches_on_file(f, patches=patches)
    
    def __run_patches_on_file(self, file, patches=[]):
        """Run all eligable (within an optional subset) CodePatches on the given file"""
        can_run_on = set()
        r: re.Pattern
        for r, funcs in self.code_res.items():
            if r.match(file):
                for f in funcs:
                    if patches and f.__name__ not in patches:
                        continue # This patch is not in the given subset, skip it
                    can_run_on.add(f)
        for f in can_run_on:
            try:
                f(file)
            except Exception as e:
                raise ValueError(f"shipfile.{f.__name__} failed on {file}: {e}")
        return

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
