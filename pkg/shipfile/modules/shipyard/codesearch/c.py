"""
Structural regex combinators for C/C++ source.

All functions return a compiled re.Pattern suitable for passing directly to
CodeFile.replace() or CodeFile.reinsert().

C includes are single-line, so find_import() is a straightforward grep.
"""

import re


def find_func(name):
    """Match the opening line of a C/C++ function definition named `name`.

        file.reinsert(c.find_func("main"), ["    setup_logging();"])
        file.replace(c.find_func("get_version"), new_impl)
    """
    return re.compile(
        r'(?m)^[^\n#/]*\b' + re.escape(name) + r'\s*\([^)]*\)\s*\{[ \t]*$'
    )


def find_func_sig(sig):
    """Build a pattern from a complete C signature string, e.g. "int main(int argc, char **argv)".

    Tolerates minor whitespace variation between tokens.

        file.reinsert(c.find_func_sig("int main(int argc, char **argv)"), ["    setup();"])
    """
    sig = sig.strip()
    m = re.match(
        r'^(?:(?P<ret>[\w][\w\s\*]*?)\s+)?(?P<name>\w+)\s*(?P<args>\(.*\))?\s*$',
        sig,
    )
    if not m:
        return re.compile(re.escape(sig) + r'\s*\{')

    parts = []
    if m.group('ret'):
        tokens = m.group('ret').split()
        esc = []
        for t in tokens:
            esc.append(re.escape(t))
        parts.append(r'\s+'.join(esc))
        parts.append(r'\s+')
    parts.append(re.escape(m.group('name')))
    if m.group('args'):
        parts.append(r'\s*' + re.escape(m.group('args')))
    else:
        parts.append(r'\s*\(.*\)')
    parts.append(r'\s*\{')
    return re.compile(''.join(parts))


def find_define(name):
    """Match a #define for macro `name` (the entire definition line).

        file.replace(c.find_define("VERSION"), '#define VERSION "2.0"')
    """
    return re.compile(r'(?m)^[ \t]*#[ \t]*define[ \t]+' + re.escape(name) + r'\b[^\n]*$')


def find_import(header):
    """Match a #include line for `header` (strips surrounding <> or "" automatically).

        file.replace(c.find_import("openssl/ssl.h"), '#include "vendor/ssl.h"')
    """
    h = re.escape(header.strip('<>"'))
    return re.compile(r'(?m)^[ \t]*#[ \t]*include[ \t]+[<"]' + h + r'[>"]')


def find_struct(name):
    """Match the opening of a struct definition for `name`.

        file.reinsert(c.find_struct("options"), ["    int custom_flag;"])
    """
    return re.compile(r'(?m)\bstruct\s+' + re.escape(name) + r'\s*\{')


def find_enum(name):
    """Match the opening of an enum definition for `name`."""
    return re.compile(r'(?m)\benum\s+' + re.escape(name) + r'\s*\{')


def find_field(type_, name):
    """Match a struct field declaration, e.g. find_field("int", "port").

        file.replace(c.find_field("int", "port"), "    uint16_t port;")
    """
    return re.compile(
        r'(?m)^[ \t]*' + re.escape(type_) + r'\s+\*?' + re.escape(name) + r'\s*;'
    )

