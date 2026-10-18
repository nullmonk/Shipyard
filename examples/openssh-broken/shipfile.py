"""
A copy of the openssh shipfile that intentionally fails during patching.
Use this to test --interactive mode:

    shipyard build --package ubuntu:25.04 --interactive examples/openssh-broken/shipfile.py
"""

import re

from shipyard import CodePatch, CodeFile, Version, Patcher, Variable

_TAG_RE = re.compile(r"^V_(?P<major>\d+)_(?P<minor>\d+)_P(?P<portable>\d+)$")


class Shipfile(Patcher):
    Name = "openssh"
    Url = "https://github.com/openssh/openssh-portable"
    PackageName = "openssh"

    VersionTags = "V_*"

    Variables = [
        Variable(
            "__BANNER__",
            "Default SSH version addendum / banner",
            "opensource is good",
            validate=lambda v: "banner must not be empty" if not v.strip() else None,
        ),
    ]

    @CodePatch(r".*servconf\.c$")
    def banner(self, file):
        # This replace will fail: the search string does not exist in the file.
        file.replace(
            "THIS_STRING_DOES_NOT_EXIST_IN_SERVCONF",
            "replacement that will never be reached",
            err="Intentional failure for --interactive testing: string not found in {file}",
        )

    @staticmethod
    def is_version_ignored(version):
        if version < "7.0":
            return True
        return False

    @staticmethod
    def tag_to_version(tag):
        m = _TAG_RE.match(tag)
        if not m:
            return "0.0"
        return "{}.{}p{}".format(m.group("major"), m.group("minor"), m.group("portable"))

    @staticmethod
    def version_to_tag(v):
        v = v.lower()
        major, rest = v.split(".", 1)
        minor, portable = rest.split("p", 1)
        return "V_" + major + "_" + minor + "_P" + portable
