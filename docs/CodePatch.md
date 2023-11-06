# CodePatches

## Basics
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


## CodePatch options

The `CodePatch` decorator takes a few optional parameters

```python
# Only run a codepatch on certain versions
@CodePatch(".*\.py", versions=["1.0.0", "1.2.0"])

# CodePatch does _not_ need to run. Default is True and will error if no file matches
@CodePatch(".*\.py", required=False)

```

## EZ Patching

EZ is an object that allows CodePatches to quickly modify source files. To use easy, throw a `with` statement in
the CodePatch. This object provides basic function that are common when patching files:
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
        # Source file has been saved with the new code
```

### Functions

Easy provides several functions for modifying the patches:

### 1. `replace(self, k: str | re.Pattern, v, err="", count=0) -> bool` - replace a string in the file with another string

`replace` can take either a string or `re.Pattern` object as the first argument:
```python
with EZ(file) as f:
    f.replace('"cat"', '"dog"', err="Hunk #1 failed: cannot find '{k}'")
    f.replace(re.Compile(r'"[Cc]at"', '"dog"'), err="Hunk #1 failed: cannot find '{k}'")
```

If `err` is specified, the function will throw a `LookupError` if replacement does not occurr. If `err` may either be a string (with optional templating for `{k}` and `{v}`) or `True` to use the default error message.

### 2. `replace_all` - identical to `replace` except it takes a dictionary of multiple items to replace

```python
with EZ(file) as f:
    f.replace_all({
        re.Compile(r'"[Cc]at"', '"dog"'), '"dog"',
        "horse": "zebra"
    })
```

### 3. `reinsert(self, regex, lines=[], before=False, err=""):` - Insert lines before or after the given regular expression
Reinsert allows lines to be inserted before or after a regular expression. This is a very common technique in patching. The
`before` argument indicates if the lines should be inserted before or after the match.

Like `replace`, `err` may either be a boolean or string. If specified, the function will throw a `LookupError` if the lines cannot be inserted or the regex cannot be found.

```python
with EZ(file) as f:
    r = r"client_version_string = xstrdup\(buf\);"
    f.reinsert(
        r, "// This is where a patch will be applied", before=True
    )
```

### Manual replacement
If these functions are not useful, EZ exposes the contents of the file and changes can be manually applied to the raw string
```python
with EZ(file) as f:
    f.contents += "\ndef whole_new_function(x):\n\treturn \"banana\"\n"
```

Contents will be saved automatically when the `with` statement is exited.