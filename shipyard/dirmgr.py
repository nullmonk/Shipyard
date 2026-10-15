import os
from typing import List

from shipyard.sources import SourceManager

class DirMgr(SourceManager):
    """A SourceManager over a plain directory on disk. It never fetches, checks out
    or diffs anything: the orchestrator hands us a finished source tree and takes
    the changes back out by diffing it itself."""
    def __init__(self, path: str, version: str = "", variables: dict = None) -> None:
        self.path = path
        self._version = version
        self.variables = variables or {}

    def _full(self, path: str) -> str:
        return os.path.join(self.path, path)

    def read(self, path: str):
        with open(self._full(path), "rb") as f:
            data = f.read()
        try:
            return data.decode("utf-8")
        except UnicodeDecodeError:
            return data

    def write(self, path: str, contents) -> None:
        if isinstance(contents, bytes):
            data = contents
        else:
            # Shipfile placeholders (e.g. __BANNER__) are resolved as the file is written
            for k, v in self.variables.items():
                contents = contents.replace(k, v)
            data = contents.encode("utf-8")
        with open(self._full(path), "wb") as f:
            f.write(data)

    def list_files(self) -> List[str]:
        files = []
        for root, dirs, names in os.walk(self.path):
            # .git is ours, .pc is quilt's bookkeeping for the distro's own patches
            dirs[:] = [d for d in dirs if d not in (".git", ".pc")]
            for n in names:
                full = os.path.join(root, n)
                if os.path.islink(full):
                    continue
                files.append(os.path.relpath(full, self.path))
        return sorted(files)

    def version(self) -> str:
        return self._version

    def versions(self) -> List[str]:
        return [self._version] if self._version else []

    def checkout(self, version: str) -> None:
        pass

    def reset(self) -> None:
        pass

    def apply(self, patch, reject=True, check=False):
        raise NotImplementedError("DirMgr only runs CodePatches, not .patch files")
