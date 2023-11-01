"""
Custom patches that are not just "diff based". Instead they match filenames and allow the user to apply code to them
"""
import re
from io import StringIO

class CodePatch:
    def __init__(self, file, *files, versions=[], **kwargs):
        self.files = [file] + list(files)
        self.versions = versions
        self.kwargs = kwargs
        self.func = None
    
    def __call__(self, func):
        setattr(func, "__patch_files", self.files)
        setattr(func, "__patch_kwargs", self.kwargs)
        return func

class Modifyable:
    def __init__(self, fname):
        self.name = fname
        with open(fname) as f:
            self.contents = f.read()
    
    def __enter__(self):
        return self
    
    def __exit__(self, exc_type, exc_value, traceback):
        if exc_type:
            return # Dont write on exits
        
        with open(self.name, 'w') as f:
            f.write(self.contents)

    def close(self):
        self.__exit__(None, None, None)
    
    def replace(self, k: str | re.Pattern, v, err="", count=0) -> bool:
        """Replace k with v in the string. If k is a re.Pattern, it will be searched. If not, a simple string replacement
        will occur. If err is defined, and the string cannot be found, 'err' will be raise as a LookupError with err templated with k and v"""
        if not isinstance(err, str):
            err = "failed to replace '{k}' with '{v}'. Not found"
        if isinstance(k, re.Pattern):
            if count <= 0:
                count = 0 # Count must be 0 for re and -1 for string
            self.contents, count = k.subn(v, self.contents, count=count)
            if count < 1:
                if err:
                    raise LookupError(err.format(k=k, v=v))
                return False
            return True
        if k not in self.contents:
            if err:
                raise LookupError(err.format(k=k, v=v))
            return False
        if count <= 0:
            count = -1 # Count must be 0 for re and -1 for string
        self.contents = self.contents.replace(k, v, count)
        return True

    def replace_all(self, replacements: dict, err="", count=0):
        """replace all strings in _replacements_ with the value. if err is defined, each string must be replaced or an error will be thrown"""
        for k, v in replacements.items():
            self.replace(k, v, err=err, count=count)
    
    def reinsert(self, regex, lines=[], before=False, err=""):
        """Insert lines before or after the given regular expression"""
        res = re.compile(regex).search(self.contents)
        if isinstance(lines, str):
            lines = [lines]
        if not res:
            if err:
                raise ValueError(err.format(regex=regex))
            return False
        
        f = StringIO()
        idx = res.end()
        if before:
            idx = res.start()
        
        f.write(self.contents[:idx])
        f.write("\n".join(lines))
        f.write(self.contents[idx:])

        self.contents = f.getvalue()
        return True