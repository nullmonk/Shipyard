from shipyard import CodePatch, CodeFile, Version, Builder

class Shipfile(Builder):
    Name = "hello"
    PackageName = "hello"
    BaseImage = "golang:1.22-alpine"
    Setup = "echo 'setup completed'"
    Build = "go build -o hello main.go"
    Artifacts = r"^hello$"

    @staticmethod
    @CodePatch(r".*main\.go$")
    def patch_greeting(file, version):
        file.replace('"Hello, World!"', '"Hello from Go Shipyard!"', err="greeting not found")
