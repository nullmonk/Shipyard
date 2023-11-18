# Handle git repositories, extract all the tags
# (versions) and check them out accordingly
import subprocess
import os

from typing import List

from shipyard.sources import SourceManager, SourceProgram
from shipyard.patch import PatchFile
from shipyard import Version

class GitMgr(SourceManager):
    def __init__(self, repo: SourceProgram) -> None:
        """
        repo: see the Repo object above
        """
        self.r = repo
        repo.Directory = repo.resolve_source_directory()
        if not os.path.exists(repo.Directory):
            print(f"[*] Cloning {repo.Url} {repo.Directory}")
            res = subprocess.run(f"git clone {repo.Url} {repo.Directory}", shell=True, encoding="utf-8")
            if res.returncode != 0:
                raise ValueError(res.stderr)
    
    def version(self) -> str:
        """Return the current version"""
        res = subprocess.run(
            ["git", "describe", "--tags"],
            capture_output=True,
            cwd=self.r.Directory,
            encoding="utf-8"
        )
        return res.stdout.strip()

    def versions(self) -> List[str]:
        res = subprocess.run(
            ["git", "--no-pager", "tag", "-l", self.r.VersionTags],
            capture_output=True,
            cwd=self.r.Directory,
            encoding="utf-8"
        )
        if res.returncode != 0:
            raise ValueError(res.stderr)
        out = res.stdout
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
        tag = self.r.version_to_tag(version)
        
        res = subprocess.run(
            f"git checkout tags/{tag}",
            shell=True,
            capture_output=True,
            cwd=self.r.Directory,
            encoding="utf-8"
        )
        if res.returncode != 0:
            raise ValueError(res.stderr)

    def apply(self, patch: PatchFile, reject=True, check=False):
        #rel = os.path.relpath(patch.Filename, self.r.Directory)
        args = ["git", "apply","-v", "--recount"]
        if reject:
            args.insert(2, "--reject")
        res = subprocess.run(
            args,
            capture_output=True,
            cwd=self.r.Directory,
            input=patch.dump(),
            encoding="utf-8"
        )
        # print(f"[{res.returncode}] {args}\n", res.stdout, res.stderr) # print debug best debug
        if res.returncode != 0:
            if check:
                return False
            raise ValueError(res.stderr)
        return True
    
    def refresh(self, patch: PatchFile = None):
        """Refresh a patch file and save it to outdir"""
        args = ["git", "--no-pager", "diff", ]
        if patch:
            args.append(patch.Index)
        res = subprocess.run(
            args,
            capture_output=True,
            cwd=self.r.Directory,
            encoding="utf-8"
        )
        if res.returncode != 0:
            raise ValueError(res.stderr)
        
        return res.stdout

    def reset(self):
        res = subprocess.run(
            f"git checkout .",
            shell=True,
            capture_output=True,
            cwd=self.r.Directory,
            encoding="utf-8"
        )
        if res.returncode != 0:
            raise ValueError(res.stderr)
    
        res = subprocess.run(
            f"git clean -fdx",
            shell=True,
            capture_output=True,
            cwd=self.r.Directory,
            encoding="utf-8"
        )
        if res.returncode != 0:
            raise ValueError(res.stderr)