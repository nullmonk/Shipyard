import os
import tempfile
from shipyard import Builder, Tester, Run, test, From, Save, Copy, Workdir


class Shipfile(Builder, Tester):
    Name = "shipyard"
    Url = "https://github.com/nullmonk/shipyard"

    # Pinned default build container (matches the in-container From() below).
    # Lets drydock standalone builds and `shipyard build --image` default
    # sensibly instead of starting from an empty container.
    BaseImage = "golang:1.26-alpine"

    def build(self):
        if os.getenv("SHIPYARD_IN_CONTAINER") == "1":
            # Container build (Dagger): fixed workdir, make(1) installed
            # (golang:alpine does not ship make).
            From("golang:1.26-alpine")
            Run("apk add --no-cache make")
            Workdir("/build")
            Copy("cmd", ".")
            Copy("pkg", ".")
            Copy("go.*", ".")
            Copy("Makefile", ".")
        else:
            # `--local` runs as the invoking user, who cannot mkdir /build:
            # stage the sources into a fresh tmp dir and build there.
            # (Copies come before Workdir: local Copy() resolves sources
            # against the current workdir.)
            tmp = tempfile.mkdtemp(prefix="shipyard-build-")
            Copy("cmd", tmp)
            Copy("pkg", tmp)
            Copy("go.mod", tmp)
            Copy("go.sum", tmp)
            Copy("Makefile", tmp)
            # build_self re-enters via `shipyard build --local shipfile.py`
            # from the workdir, so the shipfile itself must be staged too.
            Copy("shipfile.py", tmp)
            Workdir(tmp)
        Run("make build")

    @test("build self")
    def build_self(self):
        """Build ourself "locally" e.g. not in a container. Dont recurse our test"""
        if int(os.getenv("SHIPYARD_DEPTH", "0")) > 0:
            return
        Run("bin/shipyard build --local shipfile.py ")
        Run("bin/shipyard info shipfile.py ")

    def save(self):
        Save("bin/shipyard", "shipyard")