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

## Shipfile Options

#### Url
The URL of the git repo of the source

#### Urls
Occasionally, source code is not stored properly in a Git repo, instead,
several URLs may be specified. Each URL will be downloaded and tagged as a version in the source repo (see [Alternative Sources](#TODO))

#### VersionTags
A [glob(7)](https://www.man7.org/linux/man-pages/man7/glob.7.html) pattern that will match
git tags as versions. The pattern can be tested in the source repo by using the command `git tag -l 'PATTERN'`.

> Currently, the only way for Shipyard to denote versions is with git tags. This may update later if the use case comes up

#### Patches
The directory to search in for patch files. This directory will contain version folders which then contain several patch files. The directory must not be above the Shipfile in the directory tree.

#### source_directory
The directory which contains the source repo, relative to the Shipfile. Can either be a string or a function which returns a string.

#### Variables
Sometimes its useful to quickly change strings in a binary or to allow a builder to update values without touching the patchfiles. This is possible with the `Variables` dictionary in the Shipfile

```python
import uuid

class Shipfile:
    ...

    Variables = {
        "__PASSWORD__": "bananananas", # Special Password
        "__BUILD_UUID__": uuid.uuid4(), # Random UUID to put in the code
    }
```

Variables are only subsituted when running `shipyard export`.

### Functions

#### `tag_to_version(tag: str) -> str`

Take the raw string of a tag and convert it to a nicer format. This is useful for when repositories have weird tag names.

#### `version_to_tag(version) -> str`

Take the cleaned up version string and convert it to a git tag.

#### `is_version_ignored(version: shipyard.Version) -> bool`
Return True if the version should be ignored by Shipyard