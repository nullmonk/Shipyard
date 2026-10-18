from shipyard import Builder, From, Copy, Workdir, Script, Save

class Shipfile(Builder):
    Name = "hello"
    PackageName = "hello"
    Dependencies = ["glibc > 2.30"]

    def build(self):
        From("golang:1.22-alpine")
        Copy(".", "/src")
        Workdir("/src")
        Script("""
            go build -o /out/hello main.go
        """)

        Save("/out/hello")

    def save(self):
        pass
