import os
from shipyard import Builder, Tester, Run, test


class Shipfile(Builder, Tester):
    Name = "shipyard"
    Url = "https://github.com/micahjmartin/shipyard"
    VersionTags = "v*"
    BaseImage = "golang:1.26-alpine"
    ArtifactDir = "bin"
    Artifacts = r"^shipyard$"

    def build(self):
        Run("CGO_ENABLED=0 go build -ldflags='-s -w -extldflags \"-static\"' -o bin/shipyard ./cmd/shipyard")

    @test("build self")
    def build_self(self):
        """Dont recurse our test"""
        if int(os.getenv("SHIPYARD_DEPTH", "0")) > 0:
            return
        Run("bin/shipyard build --local shipfile.py")