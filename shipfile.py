import os
from shipyard import Builder, Tester, Run, test, From, Save, Copy, Workdir


class Shipfile(Builder, Tester):
    Name = "shipyard"
    Url = "https://github.com/nullmonk/shipyard"

    def build(self):
        From("golang:1.26-alpine")
        Workdir("/build")
        Copy("cmd", ".")
        Copy("pkg", ".")
        Copy("go.*", ".")
        Run("CGO_ENABLED=0 go build -ldflags='-s -w -extldflags \"-static\"' -o bin/shipyard ./cmd/shipyard")

    @test("build self")
    def build_self(self):
        """Build ourself "locally" e.g. not in a container. Dont recurse our test"""
        if int(os.getenv("SHIPYARD_DEPTH", "0")) > 0:
            return
        Run("bin/shipyard build --local shipfile.py ")
        Run("bin/shipyard info shipfile.py ")

    def save(self):
        Save("bin/shipyard", "shipyard")