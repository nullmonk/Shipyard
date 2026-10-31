from shipyard import Builder, Tester, Run, Workdir, test, SaveIfExists

class Shipfile(Builder, Tester):
    Name = "showcase"
    BaseImage = "golang:1.22-alpine"

    def setup(self):
        Run("go version")

    def build(self):
        Workdir("/showcase")
        Run("mkdir -p dist && echo 'hello from shipyard' > dist/hello.txt && cat dist/hello.txt")

    def save(self):
        SaveIfExists("dist/hello.txt", "hello.txt")

    @test("bundle-runs", "the built artifact exists and runs everywhere")
    def test_bundle(self):
        # Absolute path: staged identically in every fan-out test container.
        Run("cat /showcase/dist/hello.txt | grep -q 'hello from shipyard' && echo BUNDLE-OK")
