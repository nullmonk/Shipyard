# Handle git repositories, extract all the tags
# (versions) and check them out accordingly

import os
import inspect

from dataclasses import dataclass
from typing import List

from shipyard.patch import PatchFile

@dataclass
class SourceProgram:
    """A source program is meta information about the project to patch. See the openssh
    program for implmentation.
    
    NOTE: The implementations of this object do not need to import this type, it is
    only used internally
    """
    Name = ""
    Directory = ""
    Url = "" # Url of the software
    Patches = "patches/"
    # Test using git tag -l 'PATTERN'
    VersionTags = "*" # A pattern to match for release version tags in the source
    Variables = {}
    IgnoredVersions = []
    Urls = [] # Pass a list of urls instead of a git repo

    """If the version string is different than the git tag, do the conversions here"""
    version_to_tag = lambda _, s:s
    tag_to_version = lambda _, s:s
    is_version_ignored = lambda _: False
    _default_attributes = ("source_directory", "tag_to_version", "version_to_tag", "is_version_ignored", "Urls", "VersionTags", "Patches", "Variables")
    
    def __init__(self, name, url) -> None:
        self.source_directory = None
        self.Name = name # unused
        self.Directory = self.Name # internal only
        self.Url = url
        self._filepath = ""
    
    def resolve_source_directory(self) -> str:
        if self.source_directory:
            if callable(self.source_directory):
                return self.source_directory()
            return self.source_directory
        
        f, _ = os.path.split(self._filepath)
        f = os.path.join(f, "sources", self.Name)
        return os.path.relpath(f, ".")

    @classmethod
    def from_object(cls, obj):
        # convert our incoming object to a SourceProgram
        o = cls(obj.Name, obj.Url)
        o._filepath = inspect.getfile(obj)
        for attr in o._default_attributes:
            setattr(o, attr, getattr(obj, attr, getattr(o, attr)))
        return o

class SourceManager:
    """An object that makes sure the source code is in the right place at the right
    time. Currently we are only using git but we could extend this here if there was
    type needed"""
    def version(self) -> str:
        """Return the current version of the source code"""
        raise NotImplementedError()

    def versions(self) -> List[str]:
        """Return all the versions of the source code that we have"""
        raise NotImplementedError()

    def checkout(self, version: str) -> None:
        """Use a specific version"""
        raise NotImplementedError()

    def refresh(self, p: PatchFile, directory: str):
        """refresh a patch"""
        raise NotImplementedError()

    def apply(self, patch: PatchFile):
        """Apply a patch to the current source"""
        raise NotImplementedError()
    
    def reset(self) -> None:
        """Reset the source after changes were made"""
        raise NotImplementedError()
