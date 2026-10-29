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


class Version:
    def __init__(self, s):
        self._s = str(s) if s else ''

    def _p(self):
        import re
        return [int(n) for n in re.split('[^0-9]+', self._s) if n]

    def __str__(self): return self._s
    def __repr__(self): return 'Version(' + repr(self._s) + ')'
    def __lt__(self, o): return self._p() < Version(str(o))._p()
    def __gt__(self, o): return self._p() > Version(str(o))._p()
    def __eq__(self, o): return self._p() == Version(str(o))._p()
    def __le__(self, o): return self._p() <= Version(str(o))._p()
    def __ge__(self, o): return self._p() >= Version(str(o))._p()
    def __ne__(self, o): return self._p() != Version(str(o))._p()
    def __hash__(self): return hash(self._s)


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




