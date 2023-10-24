import os
import pathlib
import fire
template = '''"""
A file that tells shipyard where the source is located and how to properly work with it
"""
import os

# Add versions that we dont care about here
IgnoredVersions = []

class Shipfile:
    # Name of the source code
    Name = "{name}"

    # Url of the source code
    Url = "{url}"

    # Where the version folders are that store the patch files. Patches _must_ be stored in a folder with <version>
    # and will be prefixed with Patches. Leave blank if the patch folders will be sitting beside this file
    Patches = "patches/"

    # The git tags that we care about. Pulled directly from the repo
    # Test using git tag -l 'PATTERN'
    VersionTags = "*"

    @staticmethod
    def is_version_ignored(version) -> bool:
        """Return True if we want to skip this version"""
        return version in IgnoredVersions
        
    @staticmethod
    def tag_to_version(tag):
        """Needs to be able to handle every tag name returned by
            git tag -l 'PATTERN'
        """
        return tag.lower()

    @staticmethod
    def version_to_tag(v):
        return v.lower()
    
    @staticmethod
    def source_directory() -> str:
        """Override the location of the git clone"""
        # return "/some/abs/path/"
        root, _ = os.path.split(__file__)
        return os.path.relpath(os.path.join(root, "sources/{name}"), ".")

'''
def new_project(directory, url, name=""):
    """Generate a new project file for the given source <url> in <directory>"""
    if not name:
        _, name = os.path.split(url)

    if os.path.exists(os.path.join(directory, "shipfile.py")):
        raise FileExistsError(f"[!] File or directory already exists at {directory}/shipfile.py. Refusing to overwrite")

    os.makedirs(os.path.join(directory, "patches"), exist_ok=True)

    with open(os.path.join(directory, "shipfile.py"), "w") as of:
        of.write(template.format(url=url, name=name))
    with open(os.path.join(directory, ".gitignore"), "w") as of:
        of.write("sources/*")
    with open(os.path.join(directory, "patches", ".gitkeep"), "w") as of:
        of.write("Patches go here")
    
    print(f"[+] Generated new template repository at {directory}. You are all set to start creating patches!")
