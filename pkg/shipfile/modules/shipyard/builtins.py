import re


class Distro:
    def __init__(self, id="", id_like="", name="", pretty_name="", version="", version_id="", version_codename="", arch=""):
        self.id = id
        self.id_like = id_like
        self.name = name
        self.pretty_name = pretty_name
        self.version = version
        self.version_id = version_id
        self.version_codename = version_codename
        self.arch = arch


# NOTE: Version is a Go-backed type injected into the module globals by
# module.go (see version.go) -- not a Python class. gpython only dispatches
# rich comparison operators (<, <=, ==, !=, >, >=) to Go-implemented
# methods, so a pure-Python class here could never make
# `ctx.version >= "2.4.0"` or `Version("9.4p2") < 9.1` work. The name
# below must stay absent from this file so it does not shadow the Go type.


class Dependency:
    def __init__(self, names, op="", version=""):
        if isinstance(names, str):
            self.names = {"": names}
        elif isinstance(names, dict):
            self.names = {}
            for k in names:
                self.names[k] = names[k]
        else:
            self.names = {"": str(names)}
        self.op = op
        self.version = str(version) if version else ""

    def __repr__(self):
        name = self.names.get("", "")
        if not name:
            for v in self.names.values():
                name = v
                break
        if self.version:
            return "Dependency({!r}, {!r}, {!r})".format(name, self.op, self.version)
        return "Dependency({!r})".format(name)



def Dep(name, op="", version=""):
    return Dependency(name, op, version)



Variables = dict


class Context:
    def __init__(self, version="", distro=None, nproc=1, artifacts_dir=None, env=None, variables=None):
        self.version = version if isinstance(version, Version) else Version(version)
        self.distro = distro if distro is not None else Distro()
        self.nproc = nproc
        if isinstance(variables, dict):
            self.variables = variables
        elif variables is None:
            self.variables = {}
        elif isinstance(variables, list):
            self.variables = {}
            for v in variables:
                if hasattr(v, 'name'):
                    val = getattr(v, 'value', '')
                    if hasattr(v, 'validate') and callable(v.validate):
                        self.variables[v.name] = v.validate(val)
                    else:
                        self.variables[v.name] = val
                elif isinstance(v, dict) and 'name' in v:
                    self.variables[v['name']] = v.get('value', '')
        else:
            self.variables = dict(variables)


class CodeFile:
    def __init__(self, name=''):
        self.name = name
        if name:
            f = open(name, 'r')
            self.contents = f.read()
            f.close()
        else:
            self.contents = ''

    def _save(self):
        if self.name:
            f = open(self.name, 'w')
            f.write(self.contents)
            f.close()

    def _err(self, msg):
        if msg:
            try:
                msg = msg.format(file=self.name)
            except (KeyError, IndexError, ValueError):
                pass
            raise ValueError(msg)

    def replace(self, old, new, err='', count=0):
        original = self.contents
        if hasattr(old, 'sub'):
            # compiled re.Pattern
            if count:
                self.contents = old.sub(new, self.contents, count)
            else:
                self.contents = old.sub(new, self.contents)
        else:
            if count:
                self.contents = self.contents.replace(old, new, count)
            else:
                self.contents = self.contents.replace(old, new)
        if self.contents == original:
            self._err(err)
            return
        self._save()

    def replace_all(self, replacements, err='', count=0):
        for old, new in replacements.items():
            self.replace(old, new, err=err, count=count)

    def reinsert(self, regex, lines=None, before=False, err=''):
        import re
        if lines is None:
            lines = []
        if isinstance(lines, str):
            lines = [lines]

        if hasattr(regex, 'search'):
            m = regex.search(self.contents)
        else:
            m = re.search(regex, self.contents)

        if m is None:
            self._err(err)
            return

        insert_text = '\n'.join(lines) + '\n'

        if before:
            # Start of the line containing the match start
            # NOTE: gpython str has no rfind; scan forward with find instead.
            pos = 0
            nl = self.contents.find('\n')
            ms = m.start()
            while nl != -1 and nl < ms:
                pos = nl + 1
                nl = self.contents.find('\n', nl + 1)
        else:
            # After the newline that ends the line containing the match end
            pos = self.contents.find('\n', m.end())
            pos = len(self.contents) if pos == -1 else pos + 1

        self.contents = self.contents[:pos] + insert_text + self.contents[pos:]
        self._save()


def _is_valid_source_url(raw_url):
    if not raw_url or not isinstance(raw_url, str):
        return False
    u = raw_url.strip()
    if not u:
        return False
    lower = u.lower()
    if (lower.startswith("git://") or
        lower.startswith("git+http://") or
        lower.startswith("git+https://") or
        lower.startswith("git+ssh://") or
        lower.startswith("git+file://") or
        lower.startswith("ssh://git@") or
        lower.startswith("git@")):
        return True

    # Strip query and fragment
    path = u.split("?")[0].split("#")[0]
    lower_path = path.lower().rstrip("/")

    # Check git hosts
    if "://" in u:
        host = u.split("://", 1)[1].split("/", 1)[0].split("?", 1)[0].lower()
        if (host == "github.com" or host.endswith(".github.com") or
            host == "gitlab.com" or host.endswith(".gitlab.com") or
            host == "bitbucket.org" or host.endswith(".bitbucket.org") or
            host == "codeberg.org" or host.endswith(".codeberg.org") or
            host.startswith("git.")):
            return True

    if lower_path.endswith(".git") or "/git/" in lower_path:
        return True

    for ext in (".tar.gz", ".tgz", ".zip", ".tar.xz", ".txz", ".tar.bz2", ".tbz2", ".tar"):
        if lower_path.endswith(ext):
            return True

    return False


class Shipfile:
    ctx = None
    Name = ""
    Url = ""
    Urls = []
    Branch = ""
    VersionTags = "*"
    IgnoredVersions = []
    PackageName = ""
    Package = ""
    Repository = ""
    BaseImage = ""
    Patches = "patches/"
    Requires = []
    Dependencies = []
    Variables = []

    @staticmethod
    def tag_to_version(tag):
        return tag

    @staticmethod
    def version_to_tag(version):
        return version

    @classmethod
    def is_version_ignored(cls, version):
        return version in cls.IgnoredVersions

    def package_name(self):
        return self.PackageName

    def repository(self):
        return self.Repository

    def get_variables(self=None):
        import shipyard
        return shipyard.get_variables(self if self is not None else Shipfile)

    def validate(self):
        import shipyard
        shipyard.validate(self)


class Patcher(Shipfile):
    Patches = "patches/"

    def pre_patch(self): pass
    def post_patch(self): pass


class Builder(Shipfile):
    BaseImage = ""

    def setup(self): pass
    def build(self): pass


class Tester(Shipfile):
    def pre_test(self): pass
    def post_test(self): pass


def validate(obj):
    """Full validation for any Shipfile subclass. Checks base requirements first,
    then each applicable subclass in order regardless of MRO."""
    import shipyard
    cls = shipyard._get_class(obj)
    is_builder = shipyard._is_subclass(cls, Builder)
    is_patcher = shipyard._is_subclass(cls, Patcher)

    pkg = getattr(obj, "PackageName", "") or getattr(obj, "Package", "")
    if not pkg:
        pn = getattr(obj, "package_name", None)
        if callable(pn):
            try:
                pkg = pn()
            except Exception:
                pass
        elif isinstance(pn, str):
            pkg = pn
    has_pkg = True if pkg else False

    url = getattr(obj, "Url", "") or getattr(obj, "URL", "")
    urls = getattr(obj, "Urls", []) or getattr(obj, "URLS", [])
    has_url = (True if url else False) or (True if (urls and len(urls) > 0) else False)

    # A Builder with no URL is valid: it defaults to copying the local source directory.
    # Patchers and plain Shipfiles must declare a source (package or URL).
    if not (has_pkg or has_url) and not (is_builder and not is_patcher):
        raise ValueError(
            "Shipfile must define at least a source: (PackageName or sf.package_name) or (Url || Urls) with good types (git repo, tar.gz, tgz, zip)"
        )

    if url and not _is_valid_source_url(url):
        raise ValueError(
            "invalid Url '{}': must be a git repo or archive (.tar.gz, .tgz, .zip)".format(url)
        )

    if urls:
        for u in urls:
            if not _is_valid_source_url(u):
                raise ValueError(
                    "invalid Url in Urls '{}': must be a git repo or archive (.tar.gz, .tgz, .zip)".format(u)
                )

    shipyard.get_variables(obj)

    if is_patcher:
        has_codepatch = False
        for name in dir(cls):
            val = getattr(cls, name, None)
            if val is not None and hasattr(val, '__patch_files'):
                has_codepatch = True
                break
        has_pre = shipyard._has_own_attr(cls, "pre_patch")
        has_post = shipyard._has_own_attr(cls, "post_patch")
        if not (has_codepatch or has_pre or has_post):
            raise ValueError(
                "Patcher requires at least one codepatch OR a pre/post patch function"
            )
        if not shipyard._has_own_attr(cls, "save"):
            raise ValueError("Patcher requires a save() method to declare artifacts")

    if is_builder:
        has_setup = shipyard._has_own_attr(cls, "setup")
        has_build = shipyard._has_own_attr(cls, "build")
        if not (has_setup or has_build):
            raise ValueError("Builder requires a build() or setup() method")

    if shipyard._is_subclass(cls, Tester):
        has_test = False
        for name in dir(cls):
            val = getattr(cls, name, None)
            if val is not None and hasattr(val, '__test_name'):
                has_test = True
                break
        has_pre = shipyard._has_own_attr(cls, "pre_test")
        has_post = shipyard._has_own_attr(cls, "post_test")
        if not (has_test or has_pre or has_post):
            raise ValueError(
                "Tester requires at least one @test function OR a pre/post test function"
            )


# ---------------------------------------------------------------------------
# Portable multi-distro helpers (proven live on debian + centos).
#
# Deliberately generic: no software-specific functions live here (no
# sshd/openssh strings anywhere below). SaveIfExists/InstallPackage/
# InstallPackages avoid ctx / os / glob so they work in every phase:
# save() runs without file-read permission on the packer path, where
# filesystem probing would raise PermissionError. SaveIfExists instead
# calls Save() and swallows only "no files matched" errors.
# NOTE: builtins.py is go:embed-ed into the shipyard binary (module.go),
# so a binary rebuild is required before these helpers take effect.
# ---------------------------------------------------------------------------


def SaveIfExists(src, dst="."):
    """Record Save(src, dst) but ignore it when nothing matches.

    Save() errors when a glob matches zero files, so one save() cannot
    list deb + rpm + pacman patterns directly. Call once per family:
    only the patterns produced by the current distro's build take
    effect, the rest are silently skipped. Any other error is re-raised.
    """
    import shipyard
    try:
        shipyard.Save(src, dst)
    except Exception as e:
        if "no files matched" not in str(e):
            raise


def _distro_family(distro):
    """Map a Distro / ctx / id string to a package family.

    Returns one of "debian", "rpm", "suse", "alpine", "arch", or ""
    when unknown. Accepts a Distro (id/id_like), a ctx (uses .distro),
    or a plain id string. Matching mirrors GetDriver in
    pkg/builder/drivers/driver.go.
    """
    if distro is None:
        return ""
    inner = distro
    if hasattr(inner, "distro"):
        try:
            inner = inner.distro
        except Exception:
            pass
    if isinstance(inner, str):
        text = inner
    else:
        text = ""
        for attr in ("id", "id_like", "name"):
            try:
                v = getattr(inner, attr, "")
            except Exception:
                v = ""
            if v:
                text = text + " " + str(v)
    text = text.lower()
    if "suse" in text:
        return "suse"
    for key in ("debian", "ubuntu", "linuxmint", "kali"):
        if key in text:
            return "debian"
    if "alpine" in text:
        return "alpine"
    if "arch" in text:
        return "arch"
    for key in ("rocky", "alma", "centos", "redhat", "fedora", "amazon", "rhel", "rpm"):
        if key in text:
            return "rpm"
    return ""


def _sh_quote(s):
    """Single-quote s for embedding in a POSIX shell snippet."""
    return "'" + str(s).replace("'", "'\"'\"'") + "'"


def _file_operand(p):
    """Quote p as a package-file operand, adding ./ when it has no slash.

    apt/dnf/yum treat a bare `foo.deb`/`foo.rpm` as a package NAME, so file
    operands must look like paths.
    """
    s = str(p)
    if "/" not in s:
        s = "./" + s
    return _sh_quote(s)


def InstallPackages(targets, distro=None):
    """Shell: install ALL given files in ONE native-manager invocation.

    Usage: Run(InstallPackages(files, self.ctx.distro)) where files is the
    list of just-built artifact paths. A single invocation matters: with
    one call per file, a later install can resolve an already-installed
    local package from the repos and overwrite it (proven live: a dbgsym
    file's install pulled the distro's unpatched openssh-server over the
    just-installed patched one). One call pins every local file at once,
    exactly like DistroDriver.InstallArtifactsScript; the repos only fill
    genuinely missing dependencies. Non-zero exit on failure.
    distro selects the family like InstallPackage (Distro, ctx, id
    string, or omitted for runtime manager detection).
    One pinned transaction is the force mechanism: every local file
    is named explicitly so the repo can only fill genuinely missing
    deps, never substitute a same-version repo build. On apt,
    --reinstall is added as well: apt skips same-version file
    operands without it.
    """
    qs = ""
    n = 0
    for p in targets:
        if qs:
            qs = qs + " "
        qs = qs + _file_operand(p)
        n = n + 1
    if not n:
        raise ValueError("InstallPackages requires at least one file")
    fam = _distro_family(distro)
    if fam == "debian":
        return "apt-get install -y --allow-downgrades --reinstall " + qs
    if fam == "rpm":
        return (
            "if command -v dnf >/dev/null 2>&1; then dnf install -y " + qs + "; "
            "else yum install -y " + qs + "; fi"
        )
    if fam == "suse":
        return "zypper --non-interactive install --allow-unsigned-rpm " + qs
    if fam == "alpine":
        return "apk add --allow-untrusted " + qs
    if fam == "arch":
        return (
            "if command -v sudo >/dev/null 2>&1; then sudo pacman -U --noconfirm --needed " + qs + "; "
            "else pacman -U --noconfirm --needed " + qs + "; fi"
        )
    # Unknown family: detect the manager at runtime (./ prefix is valid
    # for every manager, so one normalized list serves all branches).
    return (
        "if command -v apt-get >/dev/null 2>&1; then apt-get install -y --allow-downgrades --reinstall " + qs + "; "
        "elif command -v dnf >/dev/null 2>&1; then dnf install -y " + qs + "; "
        "elif command -v yum >/dev/null 2>&1; then yum install -y " + qs + "; "
        "elif command -v zypper >/dev/null 2>&1; then zypper --non-interactive install --allow-unsigned-rpm " + qs + "; "
        "elif command -v apk >/dev/null 2>&1; then apk add --allow-untrusted " + qs + "; "
        "elif command -v pacman >/dev/null 2>&1; then "
        "if command -v sudo >/dev/null 2>&1; then sudo pacman -U --noconfirm --needed " + qs + "; "
        "else pacman -U --noconfirm --needed " + qs + "; fi; "
        "else echo \"no supported package manager\" >&2; exit 1; fi"
    )


def InstallPackage(target, distro=None):
    """Shell: install one artifact file or repo package in @test code.

    Usage: Run(InstallPackage("/path/to/file")) or
    Run(InstallPackage("openssh-server", self.ctx.distro)).
    A target that exists on disk (or carries a package suffix
    .deb/.rpm/.pkg.tar.*/.apk) is installed from file with the native
    manager so dependencies resolve; anything else is installed by name
    from the configured repos -- which assumes the test container's repos
    (including the drydock hosted mirror) are already configured.
    distro selects the family: a Distro (id/id_like), a ctx (uses
    .distro), or a plain id string. When omitted/unknown, the shell
    detects the manager at runtime (apt-get -> dnf/yum -> zypper ->
    apk -> pacman). Exits non-zero on failure.
    """
    t = _sh_quote(target)
    fam = _distro_family(distro)
    if fam == "debian":
        return (
            "t=" + t + "; "
            "case \"$t\" in *.deb) :;; *) if [ ! -e \"$t\" ]; then apt-get install -y \"$t\"; exit $?; fi;; esac; "
            "case \"$t\" in */*) f=\"$t\";; *) f=\"./$t\";; esac; "
            "apt-get install -y --allow-downgrades \"$f\""
        )
    if fam == "rpm":
        return (
            "t=" + t + "; "
            "case \"$t\" in *.rpm) :;; *) if [ ! -e \"$t\" ]; then "
            "if command -v dnf >/dev/null 2>&1; then dnf install -y \"$t\"; else yum install -y \"$t\"; fi; exit $?; fi;; esac; "
            "case \"$t\" in */*) f=\"$t\";; *) f=\"./$t\";; esac; "
            "if command -v dnf >/dev/null 2>&1; then dnf install -y \"$f\"; else yum install -y \"$f\"; fi"
        )
    if fam == "suse":
        return (
            "t=" + t + "; "
            "case \"$t\" in *.rpm) :;; *) "
            "if [ ! -e \"$t\" ]; then zypper --non-interactive install \"$t\"; exit $?; fi;; esac; "
            "zypper --non-interactive install --allow-unsigned-rpm \"$t\""
        )
    if fam == "alpine":
        return (
            "t=" + t + "; "
            "case \"$t\" in *.apk) :;; *) if [ ! -e \"$t\" ]; then apk add \"$t\"; exit $?; fi;; esac; "
            "apk add --allow-untrusted \"$t\""
        )
    if fam == "arch":
        return (
            "t=" + t + "; "
            "case \"$t\" in *.pkg.tar.*) :;; *) "
            "if [ ! -e \"$t\" ]; then "
            "if command -v sudo >/dev/null 2>&1; then sudo pacman -S --noconfirm --needed \"$t\"; "
            "else pacman -S --noconfirm --needed \"$t\"; fi; exit $?; fi;; esac; "
            "if command -v sudo >/dev/null 2>&1; then sudo pacman -U --noconfirm --needed \"$t\"; "
            "else pacman -U --noconfirm --needed \"$t\"; fi"
        )
    # Unknown family: detect the manager at runtime.
    return (
        "t=" + t + "; "
        "case \"$t\" in *.deb|*.rpm|*.pkg.tar.*|*.apk) isf=1;; *) "
        "if [ -e \"$t\" ]; then isf=1; else isf=0; fi;; esac; "
        "if [ \"$isf\" = 1 ]; then "
        "case \"$t\" in */*) f=\"$t\";; *) f=\"./$t\";; esac; "
        "if command -v apt-get >/dev/null 2>&1; then apt-get install -y --allow-downgrades \"$f\"; "
        "elif command -v dnf >/dev/null 2>&1; then dnf install -y \"$f\"; "
        "elif command -v yum >/dev/null 2>&1; then yum install -y \"$f\"; "
        "elif command -v zypper >/dev/null 2>&1; then zypper --non-interactive install --allow-unsigned-rpm \"$t\"; "
        "elif command -v apk >/dev/null 2>&1; then apk add --allow-untrusted \"$t\"; "
        "elif command -v pacman >/dev/null 2>&1; then "
        "if command -v sudo >/dev/null 2>&1; then sudo pacman -U --noconfirm --needed \"$t\"; "
        "else pacman -U --noconfirm --needed \"$t\"; fi; "
        "else echo \"no supported package manager\" >&2; exit 1; fi; "
        "else "
        "if command -v apt-get >/dev/null 2>&1; then apt-get install -y \"$t\"; "
        "elif command -v dnf >/dev/null 2>&1; then dnf install -y \"$t\"; "
        "elif command -v yum >/dev/null 2>&1; then yum install -y \"$t\"; "
        "elif command -v zypper >/dev/null 2>&1; then zypper --non-interactive install \"$t\"; "
        "elif command -v apk >/dev/null 2>&1; then apk add \"$t\"; "
        "elif command -v pacman >/dev/null 2>&1; then "
        "if command -v sudo >/dev/null 2>&1; then sudo pacman -S --noconfirm --needed \"$t\"; "
        "else pacman -S --noconfirm --needed \"$t\"; fi; "
        "else echo \"no supported package manager\" >&2; exit 1; fi; fi"
    )




