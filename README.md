<img src="logo.png" alt="Shipyard" width="400"/>  

_quilt on steriods_  


Shipyard is a tool to help build and test patches against multiple versions of a source tree. It is
designed to aid management of multiple patches that apply to multiple versions of software.

## Installation

Install system requirements
```
pacman -S git python
pip -r requirements.txt
git clone --depth=1 https://github.com/micahjmartin/shipyard
cd shipyard
pip install .
```


## Usage

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

### Building the Package

See [Building](./docs/Building.md) for more details.

## Examples
Not many examples of repositories managed with Shipyard are open source, but [free-da](https://github.com/micahjmartin/free-da) is one that makes exclusive use of
CodePatches to maintain source code


## Design Choices
Multiple different design elements were taken into consideration for this tool. These limitations quickly render
tools like `diff`, `patch`, and `quilt` difficult to use

1. Patches are inherently meant to be applied to one version of code, not multiple. Shipyard needs to be able to quickly float between versions of the software and the patches should also apply to many.
1. Patching is a very manual process when things break, Shipyard needs to be able to work side-by-side with "developers"
1. Shipyard should store patches in a way compatible with other tools (rpmbuild, debuild) and with humans (easy to manage in a filesystem)
1. Shipyard should produce buildable patchfiles and artifacts for easy integration into future tools :eyes:
1. Patch files _suck_ and often cant handle updates to the code, we might want a new system for more fluent patching
