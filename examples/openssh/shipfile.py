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

from shipyard import CodePatch, CodeFile, Version, Patcher, Tester, Run, test, Variable, SaveIfExists, InstallPackages

# Matches the modern "V_<major>_<minor>_P<portable>" tag scheme (e.g. V_9_9_P2,
# V_10_6_P1). Older releases used messier schemes (V_1_2_3_PRE5, V_2_0_0_BETA1,
# V_2_1_0_P1 with three numbers, etc.) that we don't bother supporting -
# is_version_ignored() below drops anything pre-7.0 anyway.
_TAG_RE = re.compile(r"^V_(?P<major>\d+)_(?P<minor>\d+)_P(?P<portable>\d+)$")

# Where the just-built artifacts live in the test container (absolute forms
# of the globs save() declares, plus the per-driver artifact dirs used by
# DistroDriver.InstallArtifactsScript). rpmbuild nests exactly one level
# (RPMS/<arch>/), so RPMS/*/*.rpm needs no recursive glob.

# Fixed path for the fallback host key minted in pre_test. Tests reference
# it when the system key store is unavailable (minimal containers).
_SSHD_TEST_KEY = "/tmp/shipyard-sshd-test-key"


def _sshd(args):
    # sshd invocation with binary resolution + self-contained fallback.
    # The fallback (-f /dev/null + explicit HostKey) fires only when the
    # system invocation fails (e.g. arch containers with no host keys).
    return ("if command -v sshd >/dev/null 2>&1; then S=sshd; "
            "elif [ -x /usr/sbin/sshd ]; then S=/usr/sbin/sshd; "
            "else S=/usr/bin/sshd; fi; "
            "($S " + args + " || $S " + args + " -f /dev/null -o HostKey=" + _SSHD_TEST_KEY + ")")


# Succeeds iff the freshly built openssh package is installed (package
# names are software-specific, so this lives here -- not in builtins).
# Uses whichever manager exists (dpkg -> rpm -> pacman), falling back
# to a binary presence check when none is found.
_PACKAGE_CHECK = ("if command -v dpkg >/dev/null 2>&1; then "
                  "dpkg -s openssh-server | grep -q 'Status: install ok installed'; "
                  "elif command -v rpm >/dev/null 2>&1; then "
                  "rpm -q openssh-server >/dev/null 2>&1 || rpm -q openssh >/dev/null 2>&1; "
                  "elif command -v pacman >/dev/null 2>&1; then "
                  "pacman -Q openssh >/dev/null 2>&1 || pacman -Q openssh-server >/dev/null 2>&1; "
                  "else test -x /usr/sbin/sshd || test -x /usr/bin/sshd; fi")


class Shipfile(Patcher, Tester):
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
    def banner(self, file):
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
            '\t\toptions->version_addendum = xstrdup("' + self.ctx.variables["__BANNER__"] + '");',
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

    def save(self):
        # Server + client ONLY. Deliberately excluded: the ssh metapackage
        # (deb), base openssh rpm, sftp-server, askpass, keycat, sk-dummy,
        # pam_ssh_agent_auth, and all dbgsym/debuginfo/debugsource/tests
        # byproducts. Anchors do the excluding: deb '_' (dbgsym uses '-'),
        # rpm/pacman '[0-9]' (debuginfo/debugsource/debug use a leading
        # letter). Say the word to add the ssh metapackage back.
        # Unmatched families are skipped (Save errors on zero-match globs).
        SaveIfExists("openssh-server_*.deb")  # deb server
        SaveIfExists("openssh-client_*.deb")  # deb client
        SaveIfExists("openssh-server-[0-9]*.rpm")  # rpm server, flat
        SaveIfExists("openssh-clients-[0-9]*.rpm")  # rpm clients, flat
        SaveIfExists("**/openssh-server-[0-9]*.rpm")  # rpm server, RPMS/<arch>/
        SaveIfExists("**/openssh-clients-[0-9]*.rpm")  # rpm clients, RPMS/<arch>/
        SaveIfExists("openssh-[0-9]*.pkg.tar*")  # pacman, flat
        SaveIfExists("**/openssh-[0-9]*.pkg.tar*")  # pacman, nested
        SaveIfExists("openssh-server-*.apk")  # apk server
        SaveIfExists("openssh-client-*.apk")  # apk client

    # The driver (InstallPackages: one pinned transaction, --reinstall on
    # apt) ensures the just-built files are installed; pre_test then sets
    # up the test environment (privilege-separation dir + host keys) so
    # the @tests below only fire assertions. Install is FILE-form, never
    # name-form: the drydock mirror is populated only AFTER worker success,
    # so at @test time it cannot supply the current build (name-form would
    # pull the distro's unpatched openssh and fail the banner test). The
    # installed files are the exact bits published to the mirror
    # post-success. All files go in ONE manager invocation: separate
    # installs let a later one resolve an already installed local package
    # from the repos and overwrite it (proven live: a dbgsym file's
    # install pulled the distro openssh-server over the just-installed
    # patched one). Tests install exactly what ships (server + client,
    # same set as save()): the exclusion anchors live in the patterns
    # below, so debug/extras never reach the install even though they sit
    # in the same dirs. Binary packages only: *.src.rpm files are build
    # byproducts sitting next to the binary RPMs, never installable (dnf
    # refuses source RPMs by policy), so they are filtered out after
    # globbing. Deb/pacman/apk globs need no such filter -- those output
    # dirs hold no source-package equivalent.
    def _install_built(self):
        import glob
        patterns = [
            "/tmp/build/openssh-server_*.deb",
            "/tmp/build/openssh-client_*.deb",
            "/tmp/build/openssh-server-[0-9]*.rpm",
            "/tmp/build/openssh-clients-[0-9]*.rpm",
            "/tmp/build/RPMS/*/openssh-server-[0-9]*.rpm",
            "/tmp/build/RPMS/*/openssh-clients-[0-9]*.rpm",
            "/usr/src/packages/RPMS/*/openssh-server-[0-9]*.rpm",
            "/usr/src/packages/RPMS/*/openssh-clients-[0-9]*.rpm",
            "/tmp/build/artifacts/openssh-[0-9]*.pkg.tar*",
            "/tmp/build/openssh-[0-9]*.pkg.tar*",
            "/root/packages/openssh-server-*.apk",
            "/root/packages/openssh-client-*.apk",
        ]
        files = []
        for pat in patterns:
            for f in sorted(glob.glob(pat)):
                if f.endswith(".src.rpm"):
                    continue
                if f not in files:
                    files.append(f)
        assert files, "no built openssh artifacts found"
        Run(InstallPackages(files, self.ctx.distro))

    def pre_test(self):
        self._install_built()
        # System provisioning first (works on deb/rpm lanes); minimal
        # containers (e.g. arch) silently fail it, so always also mint
        # the fixed-path fallback key the tests reference.
        Run("(mkdir -p /run/sshd 2>/dev/null || sudo mkdir -p /run/sshd 2>/dev/null || true); "
            "(ls /etc/ssh/ssh_host_* >/dev/null 2>&1 || ssh-keygen -A >/dev/null 2>&1 || sudo ssh-keygen -A >/dev/null 2>&1 || true); "
            "(rm -f " + _SSHD_TEST_KEY + " " + _SSHD_TEST_KEY + ".pub; ssh-keygen -q -t ed25519 -N '' -f " + _SSHD_TEST_KEY + " >/dev/null 2>&1 || true)")

    @test("sshd-installed", "sshd from the freshly built package is installed")
    def test_sshd_installed(self):
        Run("test -x /usr/sbin/sshd || test -x /usr/bin/sshd || test -x \"$(command -v sshd)\"")
        Run(_PACKAGE_CHECK)

    @test("sshd-config", "installed sshd_config passes sshd -t")
    def test_sshd_config(self):
        # /run/sshd is normally created by init, which containers lack;
        # pre_test created it (sudo fallback for unprivileged runners).
        Run(_sshd("-t"))

    @test("sshd-banner", "sshd advertises the patched default VersionAddendum")
    def test_banner(self):
        banner = self.ctx.variables["__BANNER__"]
        Run("(" + _sshd("-T 2>/dev/null") + ") | grep -i '^versionaddendum' | grep -qF '" + banner + "'")

    @staticmethod
    def version_to_tag(v):
        # 9.9p2 -> V_9_9_P2
        v = v.lower()
        major, rest = v.split(".", 1)
        minor, portable = rest.split("p", 1)
        return "V_" + major + "_" + minor + "_P" + portable
