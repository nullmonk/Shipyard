"""
codesearch — structural regex combinators for common languages.

Each submodule exposes find_*() functions that return re.Pattern objects
compatible with CodeFile.replace() and CodeFile.reinsert().

    from shipyard.codesearch import c, go, python as py

    file.reinsert(c.find_func("process_request"), ["    log_request();"])
    file.replace(go.find_func("getVersion"), new_impl)
    file.replace(py.find_define("VERSION"), 'VERSION = "2.0"')

Go note: imports come in single-line and block forms — use find_import() to
match an existing path and find_import_block() to inject a new one.
"""

from . import c as c
from . import go as go
from . import python as python

__all__ = ["c", "go", "python"]
