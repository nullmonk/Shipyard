"""
Custom patches that are not just "diff based". Instead they match filenames and allow the user to apply code to them
"""

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
