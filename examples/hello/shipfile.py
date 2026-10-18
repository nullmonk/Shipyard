from shipyard import Builder, From, Copy, Workdir, Script, Save

class Shipfile(Builder):
    Name = "hello"
    PackageName = "hello"
    Dependencies = ["glibc > 2.30"]

    def build(self):
        From("golang:1.22-alpine", "build")
        Copy(".", "/src")
        Workdir("/src")
        Script("""
            go build -o /out/hello main.go
        """)

        From("alpine:3.19", "final")
        Copy("build:/out/hello", "/usr/local/bin/hello")

    def save(self):
        Save("build:/out/hello", "hello")
