class CodePatch:
    """
    Custom patches that are not just "diff based". Instead they match filenames
    and allow the user to make changes to the file. Integrates best with EZ
    """
    def __init__(self, file, *files, versions=[], required=True, **kwargs):
        self.files = [file] + list(files)
        self.versions = versions
        self.required = required
        self.kwargs = kwargs
        self.func = None

    
    def __call__(self, func):
        setattr(func, "__patch_files", self.files)
        setattr(func, "__patch_kwargs", self.kwargs)
        setattr(func, "__patch_versions", self.versions)
        setattr(func, "__patch_required", self.required)
        setattr(func, "__patch_has_run", False)
        return func
