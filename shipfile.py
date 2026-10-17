from shipyard import Builder, Run


class Shipfile(Builder):
    Name = "shipyard"
    Url = "https://github.com/micahjmartin/shipyard"
    VersionTags = "v*"
    BaseImage = "golang:1.26-alpine"
    ArtifactDir = "bin"
    Artifacts = r"^shipyard$"

    def build(self):
        Run("CGO_ENABLED=0 go build -ldflags='-s -w -extldflags \"-static\"' -o bin/shipyard ./cmd/shipyard")


