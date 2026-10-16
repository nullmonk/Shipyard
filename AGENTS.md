# Shipfile Creation Instructions for Agents

To integrate a new tool into Shipyard, you must create a `shipfile.py` in the tool's directory. This file defines the `Shipfile` class, which controls how the tool's source code is fetched, versioned, and patched.

## Template

Use the following Python code as a template for `shipfile.py`. Replace `TOOL_NAME` and `GIT_URL` with the appropriate values.

```python
"""
A file that tells shipyard where the source is located and how to properly work with it
"""
import os
import re
import shutil
from shipyard import CodePatch, CodeFile, Version

# Add versions that we dont care about here
IgnoredVersions = []

class Shipfile:
    # Name of the source code (e.g., "openssh", "nmap")
    Name = "TOOL_NAME"

    # Url of the source code (Git repository URL)
    Url = "GIT_URL"

    # Where the version folders are that store the patch files.
    # Patches _must_ be stored in a folder with <version> inside this directory.
    Patches = "patches/"

    # The git tags that we care about. Pulled directly from the repo.
    # Use standard glob patterns. Test locally using: git tag -l 'PATTERN'
    VersionTags = "*"
    
    # Optional: Define variables that can be swapped out during export/build
    Variables = {
        # "__PASSWORD__": "default_password",
    }

    @staticmethod
    def is_version_ignored(version) -> bool:
        """Return True if we want to skip this version"""
        return version in IgnoredVersions
        
    @staticmethod
    def tag_to_version(tag):
        """
        Convert a git tag to a clean version string (internal representation).
        Must handle every tag name returned by git tag -l 'VersionTags'
        """
        # Example: v1.0.0 -> 1.0.0
        return tag.lstrip('v').lower()

    @staticmethod
    def version_to_tag(v):
        """
        Convert a clean version string back to a git tag.
        """
        # Example: 1.0.0 -> v1.0.0
        # If the tags don't have 'v', just return v
        return f"v{v}"
    
    @staticmethod
    def source_directory() -> str:
        """
        Location where the git repo will be cloned.
        Standard convention is 'sources/<Name>' relative to this file.
        """
        root, _ = os.path.split(__file__)
        return os.path.relpath(os.path.join(root, "sources", Shipfile.Name), ".")
```

## Key Concepts & Patterns

Based on existing implementations, follow these patterns when writing your Shipfile.

### 1. Code Patching (`@CodePatch`)
Use the `@CodePatch(regex_pattern)` decorator to target specific files.
- The regex applies to the full file path.
- The decorated function receives a `CodeFile` and, optionally, the `version` object.
- **Conditional Patching**: You can check `if version < "X.Y":` inside the function to handle code changes across different versions of the target tool.

### 2. File Editing (`CodeFile`)
The `file` argument passed into a `@CodePatch` function is a `CodeFile`. Call its methods
directly to modify the file in place — it is saved automatically when the function returns.
- **`file.replace(old, new, err="...")`**: Replaces text (`old` can be a string or a compiled regex). Always set `err` to a message so the build fails loudly if the string isn't found (prevents silent failures).
- **`file.reinsert(regex, lines, before=False, err="...")`**: Inserts lines before or after a regex match. Useful for injecting headers, variables, or function calls.
- **`file.replace_all({old: new, ...}, err="...")`**: Efficiently replace multiple patterns at once.

### 3. Version Handling
- **Non-Standard Tags**: If the repo uses tags like `release-1.0` or `version_1_0`, you **must** customize `tag_to_version` and `version_to_tag` to convert them to a clean `1.0` format and back.
- **Ignored Versions**: Use `is_version_ignored` to skip alpha/beta releases or versions known to be incompatible.

### 4. Variables
Define a `Variables` dictionary in the `Shipfile` class.
- keys should be uppercase identifiers like `__PASSWORD__` or `__HEADER_NAME__`.
- These can be used in your patches as placeholders.
- They allow builders to swap values without modifying the patch logic itself.

### 5. Injecting External Files
If you need to add new files (not just modify existing ones):
- Place the file in the tool's directory (next to `shipfile.py`).
- Inside a method (like a dummy `@CodePatch`), use `shutil.copy` to move the file from the shipfile directory into `Shipfile.source_directory`.

### 6. Generic Builders (no OS package involved)
If the tool isn't built into an OS package (`.deb`/`.rpm`/`.pkg.tar.zst`) — e.g. it's a Go, Rust,
or Node project you just need to compile — skip `Package` entirely and instead define `Build` on
the `Shipfile`. This tells Shipyard to clone `Url` itself, run your `@CodePatch`es against it,
then build it directly in a container you choose instead of using a distro's packaging tools.

```python
class Shipfile:
    Name = "TOOL_NAME"
    Url = "GIT_URL"

    BaseImage = "golang:1.22"            # container the build runs in
    Setup = "apt-get update && apt-get install -y make"  # optional, runs before Build
    Build = "go build -o TOOL_NAME ./cmd/TOOL_NAME"       # required — presence of this marks it generic
    Artifacts = r"^TOOL_NAME$"            # regex matched against ArtifactDir (defaults to the source dir)
```

Build it with `shipyard build` — the image argument becomes optional since `BaseImage` is set.