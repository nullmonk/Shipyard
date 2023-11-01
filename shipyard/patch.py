from os import path
from dataclasses import dataclass

@dataclass
class PatchFile:
    def __init__(self, contents="", filename=""):
        self.Name = ""
        self.Description = ""
        self.Index = ""
        self.FullIndex = ""
        self.Filename = filename
        self.RawHeader = ""
        self.contents: str = contents
        if filename:
            _, fname = path.split(filename)
            self.Name, _ = path.splitext(fname)
        if contents:
            self.parse()
    
    def parse(self):
        if not self.contents:
            return
        header = []
        prev = ""
        new_contents = []
        skipNext = False
        # For importing, we can skip the header
        for line in self.contents.splitlines():
            if skipNext:
                skipNext = False
                continue
            
            if line.startswith("diff --git a"):
                skipNext = True
                continue
            if line.startswith("--- "):
                header += [" "]
                self.RawHeader = "\n".join(header)
                prev = line

            if not self.RawHeader:
                if line.startswith("Index: ") or line.startswith("==================="):
                    continue
                header.append(line)
                continue
            new_contents.append(line)
            if prev.startswith("--- ") and line.startswith("+++ "):
                index = line.lstrip("+ ")
                self.FullIndex = index
            prev = line

        index = index.split(path.sep)[1:]
        self.Index = path.join(*index)
        self.contents = "\n".join(new_contents)
        self.Description = "\n".join(header).strip()
        if not header:
            return
    
    @classmethod
    def from_file(cls, filename):
        with open(filename) as f:
            return cls(f.read(), filename=filename)

    def __hash__(self) -> int:
        return hash(self.Name)
    
    def __eq__(self, other):
        if not isinstance(other, PatchFile):
            return False
        if self.Name == other.Name:
            return True
        if self.Index == other.Index:
            _, sf = path.split(self.Filename)
            _, of = path.split(other.Filename)
            if sf == of:
                return True
        return None
    
    def dump(self):
        desc = ""
        if self.Description:
            desc = self.Description+"\n"
        return f"{desc}Index: {self.FullIndex}\n{'='*67}\n{self.contents}\n"