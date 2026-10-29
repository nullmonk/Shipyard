# Local Source Workflow

Shipyard can drive the whole local patch-development loop without building
any packages or talking to Dagger: pull upstream source, jump between
versions, apply `CodePatch`es locally, sanity-check the result, and export a
plain patch file. This is independent of `shipyard build` (distro package
builds) and works fully offline once the source is pulled.

All commands take `-f/--shipfile` (default `shipfile.py`) and honor
`$SHIPYARD_VERSION` as the default `--version`.

## 1. Pull the source: `shipyard pull`

```bash
# Clone the shipfile's git Url (checks out --version/--branch if given)
shipyard pull ./sources/openssh-portable
shipyard pull ./sources/openssh-portable --version 9.6p1

# Re-pull over an existing directory
shipyard pull ./sources/openssh-portable --force
```

For shipfiles with `Urls` (tarballs, no git history) `pull` downloads every
archive and "jumpstarts" a git repo: one commit per archive, tagged with the
version parsed from the file name.

## 2. List versions: `shipyard versions`

```bash
# Remote tags filtered by VersionTags, mapped via tag_to_version
shipyard versions
shipyard versions --all        # include is_version_ignored versions

# Local tags in an already-pulled tree (works offline)
shipyard versions ./sources/openssh-portable
```

## 3. Jump versions: `shipyard checkout`

```bash
shipyard checkout 9.9p1 ./sources/openssh-portable
shipyard checkout V_9_9_P2 ./sources/openssh-portable   # raw tags work too
```

Resolves the input through `version_to_tag` (a literal tag match wins),
fetches remote tags, and checks the tag out detached. Refuses to run with
local changes unless `--force` is given. `$SHIPYARD_SOURCE` is used when no
directory is passed.

## 4. Patch locally: `shipyard patch`

```bash
# Apply CodePatches in place
shipyard patch ./sources/openssh-portable

# Dry-run: patch a scratch copy, report what would change, touch nothing
shipyard patch ./sources/openssh-portable --dry-run
```

## 5. Sanity check: `shipyard check`

```bash
shipyard check ./sources/openssh-portable
shipyard check ./sources/openssh-portable --compile   # try gcc -fsyntax-only
shipyard check ./sources/openssh-portable --strict    # fail on any advisory
```

Runs, in order:

1. `git diff --check` (whitespace errors, git checkouts only),
2. delimiter-balance scan (`()`, `[]`, `{}`) of changed C-like files,
3. with `--compile`, `gcc -fsyntax-only` on changed C files (needs the
   project's headers/configure output; off by default).

## 6. Export a patch file: `shipyard export`

```bash
shipyard export ./sources/openssh-portable -o nomnom.patch
```

Applies the CodePatches to a scratch copy and writes the unified diff
(`diff -ruN`, `.git` excluded) to stdout or `-o`. The result feeds distro
packaging (`quilt`, `debian/patches`) or plain review — no container needed.

## Example: nomnom iteration loop

```bash
shipyard pull ./sources/openssh-portable --version 9.6p1
shipyard versions                             # pick a target
shipyard checkout 9.9p1 ./sources/openssh-portable
shipyard patch ./sources/openssh-portable --dry-run
shipyard patch ./sources/openssh-portable
shipyard check ./sources/openssh-portable
shipyard export ./sources/openssh-portable -o nomnom-9.9p1.patch
```

To try the debug configure flag the nomnom shipfile adds:

```bash
cd ./sources/openssh-portable && autoreconf -fi
./configure --enable-nomnom-debug && make -j$(nproc) sshd
```
