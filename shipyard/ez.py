import re
try:
    from re import Pattern
except ImportError:
    re.Pattern = str # F old python
from io import StringIO

class EZ:
    def __init__(self, fname):
        self.name = fname
        with open(fname, 'rb') as f:
            self.contents = f.read().decode('utf8', 'replace')
    
    def __enter__(self):
        return self
    
    def __exit__(self, exc_type, exc_value, traceback):
        if exc_type:
            return # Dont write on exits
        
        with open(self.name, 'w') as f:
            f.write(self.contents)

    def close(self):
        self.__exit__(None, None, None)
    
    def replace(self, k: "str | re.Pattern", v, err="", count=0) -> bool:
        """Replace k with v in the string. If k is a re.Pattern, it will be searched. If not, a simple string replacement
        will occur. If err is defined, and the string cannot be found, 'err' will be raise as a LookupError with err templated with k and v"""
        if not isinstance(err, str):
            err = "failed to replace '{k}' with '{v}' in '{file}'. Not found"
        if isinstance(k, type(re.compile('.'))):
            if count <= 0:
                count = 0 # Count must be 0 for re and -1 for string
            self.contents, count = k.subn(v, self.contents, count=count)
            if count < 1:
                if err:
                    raise LookupError(err.format(k=k, v=v, file=self.name))
                return False
            return True
        if k not in self.contents:
            if err:
                raise LookupError(err.format(k=k, v=v, file=self.name))
            return False
        if count <= 0:
            count = -1 # Count must be 0 for re and -1 for string
        self.contents = self.contents.replace(k, v, count)
        return True

    def replace_all(self, replacements: dict, err="", count=0):
        """replace all strings in _replacements_ with the value. if err is defined, each string must be replaced or an error will be thrown"""
        for k, v in replacements.items():
            self.replace(k, v, err=err, count=count)
    
    def reinsert(self, regex: "str | re.Pattern", lines=[], before=False, err=""):
        """Insert lines before or after the given regular expression"""
        res = re.search(regex, self.contents)
        if isinstance(lines, str):
            lines = [lines]
        if not res:
            if err:
                if not isinstance(err, str):
                    err = "cannot find regex '{regex}' in {file}"
                raise LookupError(err.format(regex=regex, file=self.name))
            return False
        
        f = StringIO()
        idx = res.end()
        if before:
            idx = res.start()
        
        f.write(self.contents[:idx])
        if isinstance(lines, str):
            f.write(lines)
        else:
            f.write("\n".join(lines)+"\n")
        f.write(self.contents[idx:])

        self.contents = f.getvalue()
        return True