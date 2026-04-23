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

    def _run(self, coro_func, *args):
        try:
            # If we're in an anyio worker thread (e.g. from to_thread.run_sync)
            return anyio.from_thread.run(coro_func, *args)
        except RuntimeError:
            # If we're not in an anyio context at all
            return anyio.run(coro_func, *args)

    def read(self, path: str):
        full_path = os.path.join(self.path, path)
        async def _read():
            return await self.container.file(full_path).contents()
        return self._run(_read)

    def write(self, path: str, contents) -> None:
        full_path = os.path.join(self.path, path)
        async def _write():
            self.container = await self.container.with_new_file(full_path, contents).sync()
        self._run(_write)

    def list_files(self) -> List[str]:
        async def _list():
            return await self.container.with_workdir(self.path).with_exec(["find", ".", "-type", "f"]).stdout()
        out = self._run(_list)
        return [f[2:] if f.startswith("./") else f for f in out.splitlines() if f]

    def version(self) -> str:
        return self._version

    def versions(self) -> List[str]:
        return [self._version]

    def checkout(self, version: str) -> None:
        self.reset()

    def reset(self) -> None:
        self.container = self.base_container

    def apply(self, patch: PatchFile, reject=True, check=False):
        patch_path = os.path.join("/tmp", f"{patch.Name}.patch")

        args = ["git", "apply"]
        if reject:
            args.append("--reject")
        args.append(patch_path)

        async def _apply():
            self.container = await (
                self.container
                .with_new_file(patch_path, patch.dump())
                .with_workdir(self.path)
                .with_exec(args)
                .sync()
            )

        try:
            self._run(_apply)
        except Exception as e:
            if check:
                return False
            raise e
        return True

    def refresh(self, patch: PatchFile = None):
        args = ["git", "diff"]
        if patch:
            args.append(patch.Index)

        async def _refresh():
            return await self.container.with_workdir(self.path).with_exec(args).stdout()

        return self._run(_refresh)
