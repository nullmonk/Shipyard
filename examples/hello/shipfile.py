from shipyard import CodePatch, CodeFile, Version, Builder, Run

class Shipfile(Builder):
    Name = "hello"
    PackageName = "hello"
    Dependencies = ["glibc > 2.30"]
    BaseImage = "golang:1.22-alpine"
    Artifacts = r"^hello$"

    def setup(self):
        Run("echo 'setup completed'")

    def build(self):
        Run("go build -o hello main.go")

    @staticmethod
    @CodePatch(r".*main\.go$")
    def patch_greeting(file, version):
        file.replace('"Hello, World!"', '"Hello from Go Shipyard!"', err="greeting not found")
