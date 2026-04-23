import os
import anyio
import dagger
from typing import List
from shipyard.sources import SourceManager
from shipyard.patch import PatchFile

class DaggerMgr(SourceManager):
    def __init__(self, container: dagger.Container, path: str, version: str = "latest") -> None:
        self.base_container = container
        self.container = container
        self.path = path
        self._version = version

    def read(self, path: str):
        full_path = os.path.join(self.path, path)
        async def _read():
            return await self.container.file(full_path).contents()

        return anyio.run(_read)

    def write(self, path: str, contents) -> None:
        full_path = os.path.join(self.path, path)
        self.container = self.container.with_new_file(full_path, contents)

    def list_files(self) -> List[str]:
        async def _list():
            return await self.container.with_workdir(self.path).with_exec(["find", ".", "-type", "f"]).stdout()

        out = anyio.run(_list)
        return [f[2:] if f.startswith("./") else f for f in out.splitlines() if f]

    def version(self) -> str:
        return self._version

    def versions(self) -> List[str]:
        return [self._version]

    def checkout(self, version: str) -> None:
        self.reset()

    def reset(self) -> None:
        self.container = self.base_container

    def apply(self, patch: PatchFile):
        patch_path = os.path.join("/tmp", f"{patch.Name}.patch")
        self.container = self.container.with_new_file(patch_path, patch.dump())

        async def _apply():
            return await (
                self.container
                .with_workdir(self.path)
                .with_exec(["git", "apply", "--reject", patch_path])
                .sync()
            )

        self.container = anyio.run(_apply)

    def refresh(self, patch: PatchFile = None):
        async def _refresh():
            args = ["git", "diff"]
            if patch:
                args.append(patch.Index)
            return await self.container.with_workdir(self.path).with_exec(args).stdout()

        return anyio.run(_refresh)
