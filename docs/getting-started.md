---
title: Getting Started
layout: home
nav_order: 10
---
## Getting Started
{: .no_toc }
<details open markdown="block">
  <summary>
    Table of contents
  </summary>
  {: .text-delta }
1. TOC
{:toc}
</details>


{: .pink-title }
> Before Starting
> 
> It is highly recommended to read up on [Shipfiles]() and [Code Patches]() before continuing with this guide.

### Creating a new source
A source is simply a folder with a series of patches and a python file defining some metadata and helpers. Each source is associated with
a git repository of source code that needs patched. To create a new source run the following command:

```
shipyard init -d coreutils https://github.com/coreutils/coreutils
```

This will generate `coreutils/shipfile.py`. While this will work right out-of-the-box, a few changes will optimize our setup.

```python
# coreutils/shipfile.py
class Shipfile:
    ...
    VersionTags = "v*.*" # Change this to only look at release tags and ignore other versions
    
    @staticmethod
    def is_version_ignored(version: shipyard.Version) -> bool:
        """Do some basic version filtering"""
        if version > 8.1:
            return False
        if version == "6.5.3":
            return False
        if version = 9:
            return False
        return True
```

> Note: From here on, we can either run shipyard from within the Source directory (`coreutils/`) or by passing the source to use with `-d`.
The rest of the examples will assume operation out of the source directory


We can test our version filtering by running the following command:
```bash
shipyard versions
```

### Creating a new patch

We will now create a patch. Edit a file in `../sources/coreutils`.

View and save the patchfile and then import the patchfile into Shipyard
```bash
git diff | tee /tmp/always_root.patch
cat /tmp/always_root.patch | shipyard import_patch alway_root --description "Always show root when using 'whoami'"
# [+] Saved new patch file to scratch/alway_root.patch
```

```diff
Always show root when using 'whoami'
Index: b/src/whoami.c
===================================================================
--- a/src/whoami.c
+++ b/src/whoami.c
@@ -83,6 +83,6 @@ main (int argc, char **argv)
   if (!pw)
     error (EXIT_FAILURE, errno, _("cannot find name for user ID %lu"),
            (unsigned long int) uid);
-  puts (pw->pw_name);
+  puts ("root");
   return EXIT_SUCCESS;
 }
```

Now we will see how many versions are patch applies to. It is important to make your patches apply against as many different versions as possible.
The more versions that work, the less hassle maintence is down the road...


We will need to cleanup the source repo before moving on so we can either stash or reset our changes

```bash
shipyard checkout
# OR
cd source/coreutils
git stash 
# OR
git reset --hard && git clean -fdx
```

Check test the patch on all versions:
```bash
shipyard test_patch ../patches/scratch/always_root.patch
```

`always_root.patch` fails on most versions of the tool, we can manually check versions to see why it fails:

```bash
shipyard checkout v8.4
cd source/coreutils
git apply -v --reject ../patches/scratch/always_root.patch
# Make your changes here
```

### CodePatches
Patches _suck_ to get right, a basic change in the code can ruin an entire patchfile. To combat this, Shipyard introduces `CodePatches`. CodePatches are simple functions that apply to a file to make changes.

We can define `CodePatches` in our `shipfile.py` very easily:

```python
from shipfile import CodePatch

class Shipfile:
    ...
    @CodePatch(r".*\.c", r".*\.h") # This patch will apply to ALL .c and .h files
    def animal_converter(file: str):
        """Change all cats to dogs"""
        with open(file) as f:
            contents = f.read()
        contents = contents.replace('"cats"', '"dogs"')
        with open(file, "w") as f:
            f.write(contents)
        # Any Exceptions raised will cause the patch to fail
        if '"ape"' in contents:
            raise ValueError("animal_converter does not support 'apes'")
```

`CodePatches` can be tested like any other patch:
```bash
shipyard test_patch animal_converter
```

Because certain functions are common when patching files, a shorthand object has been provided to apply common changes:
```python
from shipfile import CodePatch, EZ

class Shipfile:
    ...
    @CodePatch(r".*\.py")
    def animal_converter(file: str):
        with EZ(file) as f:
            f.replace('"cat"', '"dog"', err="Hunk #1 failed: cannot find '{k}'")
            f.reinsert(
                r"def main\(.*\):", # the string to match on
                [
                    "\tif sys.argv[0] == 'shipyard':",
                    "\t\traise ValueError('shipyard sucks')
                ], # Lines to insert into the code
                before=False # Insert the line AFTER the regex,
                err="Cannot find main with re: {regex}"
            )
        # File has been saved with the new code here
```

### Creating new version release

When your patches are all compatible with a version, you may build that version. This will create new patches in the version directory

```
shipyard build_version v9.4
```

You may then export the patches to a single patchfile for use later
```
shipyard export v9.4
```

CodePatches and Variables will be included in the release patch file.
