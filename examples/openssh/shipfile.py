"""
This file is meant to be used with https://github.com/micahjmartin/shipyard

Patches sshd's default "VersionAddendum" so the SSH identification string
(the "server hello" sent before key exchange, RFC 4253 4.2) advertises a
custom comment:

    SSH-2.0-OpenSSH_10.6 opensource is good

HOW TO USE

In the same directory as this file, run

    shipyard versions

Pick a version to patch and run

    shipyard build_version V_9_9_P2
    shipyard export >> patchfile.diff

Or just build it directly with Dagger:

    shipyard build debian:bookworm openssh
"""

import re

from shipyard import CodePatch, CodeFile, Version, Patcher, Variable

# Matches the modern "V_<major>_<minor>_P<portable>" tag scheme (e.g. V_9_9_P2,
# V_10_6_P1). Older releases used messier schemes (V_1_2_3_PRE5, V_2_0_0_BETA1,
# V_2_1_0_P1 with three numbers, etc.) that we don't bother supporting -
# is_version_ignored() below drops anything pre-7.0 anyway.
_TAG_RE = re.compile(r"^V_(?P<major>\d+)_(?P<minor>\d+)_P(?P<portable>\d+)$")


class Shipfile(Patcher):
    Name = "openssh"
    Url = "https://github.com/openssh/openssh-portable"
    PackageName = "openssh"

    # Upstream tags look like V_9_9_P2, V_10_6_P1, etc.
    VersionTags = "V_*"

    Dependencies = ["openssh-server > 6"]

    Variables = [
        Variable(
            "__BANNER__",
            "Default SSH version addendum / banner",
            "opensource is good",
            validate=lambda v: "banner must not be empty" if not v.strip() else None,
        ),
    ]

    @CodePatch(r".*servconf\.c$")
    def banner(file, ctx):
        """
        sshd fills in the default VersionAddendum (an empty string) here,
        which is later appended to SSH_VERSION when building the
        identification string in kex.c:kex_exchange_identification(). Give
        it a default value instead of an empty one so sshd advertises our
        message without needing a sshd_config change.
        """
        file.replace(
            'if (options->version_addendum == NULL)\n'
            '\t\toptions->version_addendum = xstrdup("");',
            'if (options->version_addendum == NULL)\n'
            '\t\toptions->version_addendum = xstrdup("' + ctx.variables["__BANNER__"] + '");',
            err="Cannot find default VersionAddendum assignment in {file}",
        )

    @staticmethod
    def is_version_ignored(version):
        # servconf.c's version_addendum default didn't exist before 6.0 or so,
        # but anything pre-7.0 is ancient enough to skip.
        if version < "7.0":
            return True
        return False

    @staticmethod
    def tag_to_version(tag):
        # V_9_9_P2 -> 9.9p2 / V_10_6_P1 -> 10.6p1
        m = _TAG_RE.match(tag)
        if not m:
            # Old/pre-release tag scheme we don't support; return something
            # that is_version_ignored() will drop.
            return "0.0"
        return "{}.{}p{}".format(m.group("major"), m.group("minor"), m.group("portable"))

    @staticmethod
    def version_to_tag(v):
        # 9.9p2 -> V_9_9_P2
        v = v.lower()
        major, rest = v.split(".", 1)
        minor, portable = rest.split("p", 1)
        return "V_" + major + "_" + minor + "_P" + portable
