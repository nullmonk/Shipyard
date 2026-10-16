import os
try:
    import multiprocessing
    _default_nproc = multiprocessing.cpu_count()
except Exception:
    _default_nproc = 1

from .distro import Distro
from shipyard import Version


class Context:
    def __init__(
        self,
        version="",
        distro=None,
        nproc=None,
        artifacts_dir=None,
        env=None,
    ):
        self.version = version if isinstance(version, Version) else Version(version)
        self.distro = distro if distro is not None else Distro()
        self.nproc = nproc if nproc is not None else _default_nproc