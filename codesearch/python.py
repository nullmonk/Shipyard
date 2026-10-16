"""
Structural regex combinators for Python source.

All functions return a compiled re.Pattern suitable for passing directly to
CodeFile.replace() or CodeFile.reinsert().

Python imports come in two forms:
  direct:  import os
  from:    from os import path

find_import() matches both. find_from_import() targets only the from ... import form.
"""

import re


def find_func(name):
    """Match a Python function (or async function) definition named `name`.

        file.reinsert(py.find_func("main"), ["    setup_logging()"])
        file.replace(py.find_func("get_version"), new_impl)
    """
    return re.compile(r'(?m)^[ \t]*(?:async\s+)?def\s+' + re.escape(name) + r'\s*\(')


def find_class(name):
    """Match a Python class definition for `name`.

        file.reinsert(py.find_class("Server"), ["    debug = False"])
    """
    return re.compile(r'(?m)^[ \t]*class\s+' + re.escape(name) + r'\s*[:(]')


def find_define(name):
    """Match a module-level or class-level assignment for `name`.

    Matches:  NAME = ...  and  NAME: type = ...

        file.replace(py.find_define("VERSION"), 'VERSION = "2.0"')
    """
    return re.compile(r'(?m)^[ \t]*' + re.escape(name) + r'\s*(?::[^\n=]+)?\s*=')


def find_import(module):
    """Match an import of `module` in either direct or from-import form.

    Matches:
      import module
      import module as alias
      from module import ...
      from module.sub import ...

        file.replace(py.find_import("hashlib"), "import hashlib_ng as hashlib")
    """
    escaped = re.escape(module)
    return re.compile(
        r'(?m)^[ \t]*(?:from\s+' + escaped + r'(?:\.\w+)*\s+import|import\s+' + escaped + r'(?:\s+as\s+\w+)?)\b'
    )


def find_from_import(module, name):
    """Match a specific 'from module import name' statement.

        file.replace(py.find_from_import("os.path", "join"), "from pathlib import Path")
    """
    return re.compile(
        r'(?m)^[ \t]*from\s+' + re.escape(module) + r'\s+import\s+[^\n]*\b' + re.escape(name) + r'\b'
    )


def find_decorator(name):
    """Match a decorator line @name (with or without arguments).

        file.replace(py.find_decorator("deprecated"), "@deprecated_v2")
    """
    return re.compile(r'(?m)^[ \t]*@' + re.escape(name) + r'(?:\s*\(|[ \t]*$)')
