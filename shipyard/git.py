# Handle git repositories, extract all the tags
# (versions) and check them out accordingly
import subprocess
import os

from typing import List

from shipyard.sources import SourceManager, SourceProgram
from shipyard.patch import PatchFile
from shipyard.version import Version

class GitMgr(SourceManager):
    def __init__(self, repo: SourceProgram) -> None:
        """
        repo: see the Repo object above
        """
        self.r = repo
        repo.Directory = repo.resolve_source_directory()

    def _git(self, *args, input=None, check=True) -> subprocess.CompletedProcess:
        """Run git in the source directory. Raises ValueError with git's stderr on failure if check is set"""
        res = subprocess.run(
            ["git", *args],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            cwd=self.r.Directory,
            input=input,
            encoding="utf-8"
        )
        if check and res.returncode != 0:
            raise ValueError(res.stderr)
        return res

    def prepare(self):
        """Ensure we have the source code when we need it"""
        if not os.path.exists(self.r.Directory):
            print(f"[*] Cloning {self.r.Url} {self.r.Directory}")
            res = subprocess.run(["git", "clone", self.r.Url, self.r.Directory], encoding="utf-8")
            if res.returncode != 0:
                raise ValueError(res.stderr)

    def read(self, path: str):
        self.prepare()
        fpath = os.path.join(self.r.Directory, path)
        with open(fpath, "rb") as f:
            data = f.read()
        try:
            return data.decode("utf-8")
        except UnicodeDecodeError:
            return data

    def write(self, path: str, contents) -> None:
        self.prepare()
        fpath = os.path.join(self.r.Directory, path)
        os.makedirs(os.path.dirname(fpath), exist_ok=True)
        if isinstance(contents, bytes):
            with open(fpath, "wb") as f:
                f.write(contents)
        else:
            with open(fpath, "w", encoding="utf-8") as f:
                f.write(contents)

    def list_files(self) -> List[str]:
        self.prepare()
        return self._git("ls-files").stdout.splitlines()

    def version(self) -> str:
        """Return the current version"""
        self.prepare()
        return self._git("describe", "--tags", check=False).stdout.strip()

    def versions(self) -> List[str]:
        self.prepare()
        out = self._git("--no-pager", "tag", "-l", self.r.VersionTags).stdout
        versions = []
        for tag in out.split():
            version = Version(self.r.tag_to_version(tag))
            if version and not self.r.is_version_ignored(version):
                versions.append(version)
        if not versions:
            versions = ["HEAD"]
        return sorted(versions)

    def checkout(self, version) -> None:
        """Make sure we have the correct version of the code sitting at
        self.r.Directory after this function is called. In our case its a git-checkout
        in other scenarios it might be a wget/etc"""
        self.prepare()
        self._git("checkout", f"tags/{self.r.version_to_tag(version)}")

    def apply(self, patch: PatchFile, reject=True, check=False):
        self.prepare()
        args = ["apply", "-v", "--recount"]
        if reject:
            args.insert(1, "--reject")
        res = self._git(*args, input=patch.dump(), check=False)
        if res.returncode != 0:
            if check:
                return False
            raise ValueError(res.stderr)
        return True

    def refresh(self, patch: PatchFile = None):
        """Refresh a patch file and return the results"""
        self.prepare()
        args = ["--no-pager", "diff"]
        if patch:
            args.append(patch.Index)
        return self._git(*args).stdout

    def reset(self):
        self.prepare()
        self._git("checkout", ".")
        self._git("clean", "-fdx")
