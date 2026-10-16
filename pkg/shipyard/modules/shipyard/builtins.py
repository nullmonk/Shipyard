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


class Context:
    def __init__(self, version="", distro=None, nproc=1, artifacts_dir=None, env=None):
        self.version = version if isinstance(version, Version) else Version(version)
        self.distro = distro if distro is not None else Distro()
        self.nproc = nproc


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
            raise ValueError(msg.format(file=self.name))

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
            line_start = self.contents.rfind('\n', 0, m.start())
            pos = 0 if line_start == -1 else line_start + 1
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
    Name = ""
    Url = ""
    Urls = []
    Branch = ""
    VersionTags = "*"
    IgnoredVersions = []
    Artifacts = ""
    ArtifactDir = ""
    Variables = {}
    PackageName = ""
    Package = ""
    Repository = ""
    BaseImage = ""
    Setup = ""
    Build = ""
    Patches = "patches/"

    @staticmethod
    def tag_to_version(tag):
        return tag

    @staticmethod
    def version_to_tag(version):
        return version

    @classmethod
    def is_version_ignored(cls, version):
        return version in cls.IgnoredVersions

    def package_name(self, ctx=None):
        return self.PackageName

    def repository(self, ctx=None):
        return self.Repository

    def validate(self):
        pkg = getattr(self, "PackageName", "") or getattr(self, "Package", "")
        if not pkg:
            pn = getattr(self, "package_name", None)
            if callable(pn):
                try:
                    pkg = pn(None)
                except Exception:
                    pass
            elif isinstance(pn, str):
                pkg = pn
        has_pkg = bool(pkg)

        url = getattr(self, "Url", "") or getattr(self, "URL", "")
        urls = getattr(self, "Urls", []) or getattr(self, "URLS", [])
        has_url = bool(url) or (bool(urls) and len(urls) > 0)

        if not (has_pkg or has_url):
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


class Patcher(Shipfile):
    Patches = "patches/"

    def pre_patch(self, ctx=None): pass
    def post_patch(self, ctx=None): pass

    def validate(self):
        super().validate()
        cls = self if isinstance(self, type) else type(self)
        has_codepatch = False
        for name in dir(cls):
            val = getattr(cls, name, None)
            if val is not None and hasattr(val, '__patch_files'):
                has_codepatch = True
                break
        has_pre = getattr(cls, "pre_patch", None) is not Patcher.pre_patch
        has_post = getattr(cls, "post_patch", None) is not Patcher.post_patch
        if not (has_codepatch or has_pre or has_post):
            raise ValueError(
                "Patcher requires at least one codepatch OR a pre/post patch function"
            )


class Builder(Shipfile):
    BaseImage = ""

    def setup(self, ctx=None): pass
    def build(self, ctx=None): pass

    def validate(self):
        super().validate()
        artifacts = getattr(self, "Artifacts", "")
        if not artifacts:
            raise ValueError("Builder requires an artifacts regex")
        try:
            re.compile(artifacts)
        except Exception as e:
            raise ValueError("Builder Artifacts regex '{}' is invalid: {}".format(artifacts, e))

        cls = self if isinstance(self, type) else type(self)
        setup_val = getattr(self, "Setup", "")
        build_val = getattr(self, "Build", "")
        has_setup = bool(setup_val) or (getattr(cls, "setup", None) is not Builder.setup)
        has_build = bool(build_val) or (getattr(cls, "build", None) is not Builder.build)
        if not (has_setup or has_build):
            raise ValueError("Builder requires setup or build (script or function)")
