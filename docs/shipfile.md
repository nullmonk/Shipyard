---
title: Shipfile Reference
layout: home
nav_order: 2
---
# Shipfile
{: .no_toc }
<details open markdown="block">
  <summary>
    Table of contents
  </summary>
  {: .text-delta }
1. TOC
{:toc}
</details>


A Shipfile is the workhorse of Shipyard. It controls how patches get applied, defines [CodePatches](./CodePatches.md), and defines metadata about the source. To generate a new
shipfile for a source, run the following. The URL will be the path to the source repo

```bash
# Init the file in this folder
shipyard init "<source-git-repo>"
# Init the file in another folder
shipyard -d MyProject init "<source-url>"
```

This sample Shipfile documents each option and function that can be set in a Shipfile class.

```python
# shipfile.py

import os
import re
from shipyard import CodePatch, EZ, Version

class Shipfile:
    Name = "coreutils" # Doesnt actually do anything

    # The URL of the git repo of the source
    Url = "https://github.com/coreutils/coreutils"

    # TODO: Jumpstart: Each URL will be downloaded and tagged as a version in the source repo
    Urls = [
        "https://git.savannah.gnu.org/cgit/coreutils.git/snapshot/coreutils-9.4.tar.gz",
        "https://git.savannah.gnu.org/cgit/coreutils.git/snapshot/coreutils-9.3.tar.gz",
        "https://git.savannah.gnu.org/cgit/coreutils.git/snapshot/coreutils-9.2.tar.gz",
        "https://git.savannah.gnu.org/cgit/coreutils.git/snapshot/coreutils-9.1.tar.gz",
        "https://git.savannah.gnu.org/cgit/coreutils.git/snapshot/coreutils-9.0.tar.gz",
    ]


    """
    The directory to search in for patch files. This directory will contain version folders which
    then contain several patch files. The directory must not be above the Shipfile in the
    directory tree.
    """
    Patches = "patches"

    """
    A [glob(7)](https://www.man7.org/linux/man-pages/man7/glob.7.html) pattern that will match git
    tags as versions. The pattern can be tested in the source repo by using the command
    `git tag -l 'PATTERN'`.
    
    Only versions matching this tag will be seen by shipyard
     
    Currently, the only way for Shipyard to denote versions is with git tags. This may update later
    if the use case comes up
    """
    VersionTags = "v*.*"
    
    """
    Sometimes its useful to quickly change strings in a binary or to allow a builder to update
    values without touching the patchfiles. Variables substitutes each key in the exported patch
    file with the corresponding value.
    """
    Variables = {
        "__BUILD_UUID__": uuid.uuid4(), # Random UUID to put in the code
        "__ANIMAL__": "dog",
    }

    @staticmethod
    def is_version_ignored(version: Version) -> bool:
        """Return True if the version should be ignored by Shipyard"""
        if version < 8.32:
            return True
        return False
        
    @staticmethod
    def tag_to_version(tag):
        """
        Take the raw string of a tag and convert it to a nicer format. This is useful for when repositories have weird tag names.
        """
        return tag

    @staticmethod
    def version_to_tag(v: str):
        """
        Take the cleaned up version string and convert it to a git tag.
        """
        return v
    
    @staticmethod
    def source_directory() -> str:
        """Override the location of the git clone
        
        source_directory can also be a string:
            source_dirctory = "sources/coreutils"
        """
        root, _ = os.path.split(__file__)
        return os.path.relpath(os.path.join(root, "sources", "coreutils"), ".")

    def pre_patches(self):
        """This function is called just before the patches are applied"""
        return
    
    def post_patches(self):
        """This function is called just after the patches are applied"""
        return
    
    @CodePatch(r".*main\.c")
    def test(file, version: Version):
        """The shipyard.Version object is a special object that can be compared against a float, int,
        string, or list. Python uses lexicographical comparisons for lists of numbers, and the
        Version object handles these.

        Examples of comparisons:
            v = Version("v8.9")
            v > 7 = True
            v == 8.9 = True
            v < "9.1.1" = True

        See more here
        https://docs.python.org/3/tutorial/datastructures.html#comparing-sequences-and-other-types
        """
        # Skip certain versions for certain files
        if version < 8:
            with EZ(file) as ez:
                ez.replace('"cats"', '"__ANIMAL__"') # Will get replaced when exported as a patch
```