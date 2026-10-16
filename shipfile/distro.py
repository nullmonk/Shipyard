"""
Distro identifies the distro and target architecture a build is running
for, modeled on /etc/os-release (see os-release(5)). Every field defaults
to empty/unknown.
"""


class Distro:
    def __init__(
        self,
        id="",
        id_like="",
        name="",
        pretty_name="",
        version="",
        version_id="",
        version_codename="",
        arch="",
    ):
        self.id = id  # e.g. "debian", "rocky", "arch"
        self.id_like = id_like  # e.g. "debian" (ubuntu), "rhel fedora" (rocky)
        self.name = name  # e.g. "Debian GNU/Linux"
        self.pretty_name = pretty_name  # e.g. "Debian GNU/Linux 12 (bookworm)"
        self.version = version  # e.g. "12 (bookworm)"
        self.version_id = version_id  # e.g. "12", "9.3", "22.04"
        self.version_codename = version_codename  # e.g. "bookworm", "jammy"
        self.arch = arch  # target architecture, e.g. "amd64", "arm64"
