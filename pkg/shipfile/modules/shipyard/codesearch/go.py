"""
Structural regex combinators for Go source.

All functions return a compiled re.Pattern suitable for passing directly to
CodeFile.replace() or CodeFile.reinsert().

Go imports come in two forms:
  single-line:  import "fmt"
  block:        import (
                    "fmt"
                    "os"
                )

find_import() matches an import path in either form.
find_import_block() targets the block opening — use it with reinsert() to
inject a new import without disturbing the existing ones.
"""

import re


def find_func(name):
    """Match a Go top-level function definition named `name`.

        file.reinsert(go.find_func("main"), ["\\tsetupLogging()"])
        file.replace(go.find_func("getVersion"), new_impl)
    """
    return re.compile(r'(?m)^func\s+' + re.escape(name) + r'\s*[\(\[]')


def find_method(receiver_type, name):
    """Match a Go method on `receiver_type` named `name`.

    Matches both value and pointer receivers.

        file.reinsert(go.find_method("Server", "ServeHTTP"), ["\\tlog.Println(r.URL)"])
    """
    return re.compile(
        r'(?m)^func\s*\(\s*\w+\s+\*?' + re.escape(receiver_type) + r'\s*\)\s+'
        + re.escape(name) + r'\s*\('
    )


def find_define(name):
    """Match a Go const or var declaration for `name` (single-line or inside a block).

        file.replace(go.find_define("DefaultPort"), "\\tDefaultPort = 9090")
    """
    return re.compile(r'(?m)^\s*' + re.escape(name) + r'\s*(?:[A-Z]\w*)?\s*=')


def find_import(pkg):
    """Match an import path `pkg` in either single-line or block form.

    Single-line:  import "pkg"
    Block entry:       "pkg"   (indented inside import (...))

    Use find_import_block() instead when you need to *add* a new import.

        file.replace(go.find_import("crypto/md5"), '\\t"crypto/sha256"')
    """
    escaped = re.escape(pkg)
    return re.compile(
        r'(?m)(?:^import\s+"' + escaped + r'"|^\s+"' + escaped + r'")'
    )


def find_import_block():
    """Match the opening of a grouped import block: import (

    Use with reinsert() to inject a new import path after the opening paren.

        file.reinsert(go.find_import_block(), ['\\t"newpackage"'])
    """
    return re.compile(r'(?m)^import\s*\(')


def find_struct(name):
    """Match a Go struct type definition for `name`.

        file.reinsert(go.find_struct("Config"), ["\\tDebugMode bool"])
    """
    return re.compile(r'(?m)^type\s+' + re.escape(name) + r'\s+struct\s*\{')


def find_interface(name):
    """Match a Go interface type definition for `name`."""
    return re.compile(r'(?m)^type\s+' + re.escape(name) + r'\s+interface\s*\{')


def find_type(name):
    """Match any Go type definition for `name` (struct, interface, alias, etc.)."""
    return re.compile(r'(?m)^type\s+' + re.escape(name) + r'\s+\w')

