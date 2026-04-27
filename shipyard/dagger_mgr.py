import os
import anyio
import dagger
from typing import List
from shipyard.sources import SourceManager
from shipyard.patch import PatchFile

class DaggerMgr(SourceManager):
    def __init__(self, container: dagger.Container, path: str, version: str = "latest", client: dagger.Client = None) -> None:
        self.base_container = container
        self.container = container
        self.path = path
        self._version = version
        self.client = client or container.client

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

        async def _apply():
            # First write the patch file
            ctr = self.container.with_new_file(patch_path, patch.dump()).with_workdir(self.path)

            # Try git apply first
            git_args = ["git", "apply", "--whitespace=nowarn"]
            if reject:
                git_args.append("--reject")
            git_args.append(patch_path)

            try:
                self.container = await ctr.with_exec(git_args).sync()
                return True
            except Exception:
                # Fallback to patch command
                patch_args = ["patch", "-p1", "-i", patch_path]
                try:
                    self.container = await ctr.with_exec(patch_args).sync()
                    return True
                except Exception as e:
                    if check:
                        return False
                    raise e

        return self._run(_apply)

    def refresh(self, patch: PatchFile = None):
        async def _refresh():
            if not self.client:
                 raise ValueError("Dagger client is required for refresh()")

            base_dir = self.base_container.directory(self.path)
            current_dir = self.container.directory(self.path)

            # diff -Naur /a /b
            # We use alpine to ensure we have a clean environment with diff installed
            # We use a shell wrapper to handle exit code 1 (changes found) as success
            diff_ctr = (
                self.client.container()
                .from_("alpine")
                .with_exec(["apk", "add", "diffutils"])
                .with_directory("/a", base_dir)
                .with_directory("/b", current_dir)
                .with_exec(["sh", "-c", "diff -Naur /a /b; ret=$?; if [ $ret -gt 1 ]; then exit $ret; fi; exit 0"])
            )

            try:
                res = await diff_ctr.stdout()
            except Exception:
                res = ""

            # Post-process to fix prefixes /a/ -> a/ and /b/ -> b/
            lines = res.splitlines()
            new_lines = []
            for line in lines:
                if line.startswith("--- /a"):
                    new_lines.append("--- a" + line[6:])
                elif line.startswith("+++ /b"):
                    new_lines.append("+++ b" + line[6:])
                else:
                    new_lines.append(line)

            if not new_lines:
                return ""
            return "\n".join(new_lines) + "\n"

        return self._run(_refresh)
