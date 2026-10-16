"""
Shipfile is the common base: who the project is, where its pristine code
comes from, how to make sense of its version tags, and where to find build
artifacts once something produces them. Patcher and Builder are the two
subclasses -- inherit one, or both if a project needs to patch and then
build itself.
"""

import os
import re


class Shipfile:
    """Identity, source location, versioning and artifact location. Inherit
    Patcher and/or Builder -- never this directly."""

    Name = ""

    # Where the pristine source comes from. Mutually exclusive.
    Url = ""
    Urls = []
    Branch = ""  # Url (git) mode only: branch to track if not the default branch

    VersionTags = "*"  # glob tested with: git tag -l 'PATTERN'
    IgnoredVersions = []  # versions is_version_ignored() should drop

    Artifacts = ""  # regex matched against build output paths to decide what to export
    ArtifactDir = ""  # where to look for Artifacts; defaults to the source directory

    Variables = {}  # placeholder substitutions (e.g. __PASSWORD__), applied when exporting/writing

    # Used instead of URL/URLS to source from a distro's own package manager.
    PackageName = ""  # default package name
    Repository = ""  # custom mirror/repo to fetch the package from, instead of the distro default

    @staticmethod
    def tag_to_version(tag):
        """Convert a git tag into a clean version string."""
        return tag

    @staticmethod
    def version_to_tag(version):
        """Convert a clean version string back into a git tag."""
        return version

    @classmethod
    def is_version_ignored(cls, version):
        """Return True to skip this version (alphas/betas/pre-releases, ...)."""
        return version in cls.IgnoredVersions

    def package_name(self, ctx):
        """Name passed to the distro's source-fetch command (apt-get source,
        yumdownloader --source, pkgctl repo clone, ...). Override to vary it
        by ctx.distro/ctx.arch; defaults to PackageName."""
        return self.PackageName

    def repository(self, ctx):
        """Custom mirror/repo to fetch the distro source from. Empty string
        means use the distro's default."""
        return self.Repository

    def validate(self):
        """Validate that the shipfile satisfies source requirements."""
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

    path = u.split("?")[0].split("#")[0]
    lower_path = path.lower().rstrip("/")

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


class Patcher(Shipfile):
    """A set of functions that modify the prepared source: CodePatches (just
    @CodePatch(...)-decorated methods defined directly on the subclass body,
    discovered automatically) and raw .patch files, plus hooks to run before
    and after. Builds nothing on its own -- the fit for a project you only
    want to track and patch (`shipyard export` / `build_version`)."""

    Patches = "patches/"  # directory holding the per-version raw .patch files

    def pre_patch(self, ctx):
        """Runs once, before any CodePatch or raw patch is applied."""

    def post_patch(self, ctx):
        """Runs once, after every CodePatch and raw patch has been applied."""

    def apply_patches(self, source_dir, version='', ctx=None):
        """Discover and invoke every @CodePatch method on this class against
        the files in source_dir, wrapped by pre_patch / post_patch.

        version is passed as the second argument to each patch function and
        used to filter patches that declare a versions= constraint.
        """
        from shipyard import CodeFile, Version
        from .context import Context

        if ctx is None:
            ctx = Context(version=version)

        v = Version(version)
        cls = type(self)

        self.pre_patch(ctx)

        for name in vars(cls):
            # getattr unwraps @staticmethod / @classmethod descriptors
            fn = getattr(cls, name)
            if not callable(fn) or not hasattr(fn, '__patch_files'):
                continue

            # Version filter: only run if __patch_versions is empty or contains v
            patch_versions = getattr(fn, '__patch_versions', ())
            if patch_versions:
                if not any(Version(str(pv)) == v for pv in patch_versions):
                    continue

            required = getattr(fn, '__patch_required', True)
            patterns = fn.__patch_files  # tuple of regex strings

            matched = []
            for dirpath, _dirs, filenames in os.walk(source_dir):
                for filename in filenames:
                    filepath = os.path.join(dirpath, filename)
                    norm = filepath.replace('\\', '/')
                    if any(re.search(str(p), norm) for p in patterns):
                        matched.append(filepath)

            if required and not matched:
                raise RuntimeError(
                    "CodePatch '{}' matched no files under '{}' (patterns: {})".format(
                        name, source_dir, [str(p) for p in patterns]
                    )
                )

            for filepath in matched:
                fn(CodeFile(filepath), v)

        self.post_patch(ctx)

    def validate(self):
        """Validate that the patcher satisfies patcher requirements."""
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
    """Builds the prepared source itself, in a container you choose, instead
    of relying on a distro's packaging tools. Think fancy makefile."""

    BaseImage = ""  # container image to build in, e.g. "golang:1.22"

    def setup(self, ctx):
        """Optional setup step that prepares the build environment.
        Runs once, before build(). No-op by default."""

    def build(self, ctx):
        """Builds the project. ctx.root is the patched source, already the
        working directory. Override to branch on ctx.distro/ctx.arch/ctx.version."""
        raise NotImplementedError("{} must implement build()".format(type(self).__name__))

    def run_build(self, source_dir, version='', ctx=None):
        """Run the setup → build pipeline with Run() already unlocked by the
        caller. A Context is created from version if one isn't provided."""
        from .context import Context

        if ctx is None:
            ctx = Context(version=version)

        self.setup(ctx)
        self.build(ctx)

    def validate(self):
        """Validate that the builder satisfies builder requirements."""
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
