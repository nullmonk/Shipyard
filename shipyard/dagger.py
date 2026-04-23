import os
import anyio
import dagger
from typing import List
from shipyard.sources import SourceManager
from shipyard.patch import PatchFile

class DaggerMgr(SourceManager):
    def __init__(self, container: dagger.Container, path: str = "", version: str = "latest") -> None:
        self.base_container = container
        self.container = container
        self.path = path
        self._version = version

    async def _get_path(self) -> str:
        if self.path:
            return self.path

        # Try to read from SHIPYARD_SOURCE env in container
        env = await self.container.env_variables()
        for e in env:
            if await e.name() == "SHIPYARD_SOURCE":
                self.path = await e.value()
                return self.path

        raise ValueError("No path provided and SHIPYARD_SOURCE not set in container")

    async def read(self, path: str):
        workdir = await self._get_path()
        full_path = os.path.join(workdir, path)
        return await self.container.file(full_path).contents()

    async def write(self, path: str, contents) -> None:
        workdir = await self._get_path()
        full_path = os.path.join(workdir, path)
        self.container = await self.container.with_new_file(full_path, contents).sync()

    async def list_files(self) -> List[str]:
        workdir = await self._get_path()
        out = await self.container.with_workdir(workdir).with_exec(["find", ".", "-type", "f"]).stdout()
        return [f[2:] if f.startswith("./") else f for f in out.splitlines() if f]

    def version(self) -> str:
        return self._version

    def versions(self) -> List[str]:
        return [self._version]

    async def checkout(self, version: str) -> None:
        self.reset()

    def reset(self) -> None:
        self.container = self.base_container

    async def apply(self, patch: PatchFile, reject=True, check=False):
        workdir = await self._get_path()
        patch_path = os.path.join("/tmp", f"{patch.Name}.patch")
        self.container = self.container.with_new_file(patch_path, patch.dump())

        args = ["git", "apply"]
        if reject:
            args.append("--reject")
        args.append(patch_path)

        try:
            self.container = await (
                self.container
                .with_workdir(workdir)
                .with_exec(args)
                .sync()
            )
        except Exception as e:
            if check:
                return False
            raise e
        return True

    async def refresh(self, patch: PatchFile = None):
        workdir = await self._get_path()
        args = ["git", "diff"]
        if patch:
            args.append(patch.Index)
        return await self.container.with_workdir(workdir).with_exec(args).stdout()
