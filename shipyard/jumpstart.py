import os
import re
import requests
import shutil
import subprocess
import tempfile

from typing import List
from shipyard.version import Version

def jumpstart(dest: str, urls: List[str]):
    """Make sure all the URLs are downloaded into the git repo and tagged properly"""
    if not os.path.isdir(dest):
        os.makedirs(dest, exist_ok=True)
    # can be run several times
    if not os.path.isdir(os.path.join(dest, '.git')):
        subprocess.run("git init", shell=True, cwd=dest)

    # get the versions in the repo
    res = subprocess.run(
        ["git", "--no-pager", "tag", "-l", ],
        capture_output=True,
        cwd=dest,
        encoding="utf-8"
    )
    if res.returncode != 0:
        raise ValueError(res.stderr)

    versions = res.stdout.split()
    for url, version, fmt in extensions(urls):
        if version in versions:
            continue # Skip ones we already have
        download_version(url, version, fmt, dest)

def commit(dest, msg, version=None):
    r = subprocess.run(f'git add -Av', shell=True, cwd=dest, capture_output=True, encoding="utf-8")
    if r.returncode != 0:
        raise ValueError("git-add: "+r.stderr+r.stdout)
    r = subprocess.run(f'git commit -m "{msg}"', shell=True, cwd=dest, capture_output=True, encoding="utf-8")
    if r.returncode != 0:
        raise ValueError("git-commit: "+r.stderr+r.stdout)
    if version:
        r = subprocess.run(['git', "tag", "-a", version, "-m", msg], cwd=dest, capture_output=True, encoding="utf-8")
        if r.returncode != 0:
            raise ValueError("git-tag: "+r.stderr+r.stdout)

def extensions(urls) -> List[tuple]:
    """Get the version, and type from each url
    [(url, version, type)]
    """
    reg = re.compile(r"[^\d]*(.+)")
    results = []
    for u in urls:
        _, v = os.path.split(u)
        match = reg.match(v)
        if not match:
            raise ValueError(f"version could not be determined from '{v}'")
        fmt, ext = select_format(v)
        if not fmt:
            raise ValueError("Could not detect file format as determined by shutil")
        
        results.append((
            u, Version(match.group(1)[:-len(ext)]), fmt
        ))

    # Sort the results by version
    results.sort(key=lambda x: x[1])
    return results

def select_format(url: str) -> (str, str):
    for e in shutil.get_unpack_formats():
        for ext in e[1]:
            if url.endswith(ext):
                return e[0], ext
    return None, None


def download_version(url: str, version: str, fmt: str, dest: str):
    """A new release does a few things.
    1. delete all files that dont start with .git
    2. downloads and extract contents into the git
    3. Commit the stuff, tag it
    """
    # Check if we have unstaged changes, if so, add them to a commit
    r = subprocess.run("git diff", cwd=dest, shell=True, capture_output=True)
    if len(r.stdout.splitlines()) > 0:
        print(f"[!] Unstaged changes exist in {dest}. Refusing to delete")
        print(r.stdout)
        exit(1)

    for f in os.listdir(dest):
        if not f.startswith(".git"):
            f = os.path.join(dest, f)
            if os.path.isfile(f):
                os.remove(f)
            elif os.path.isdir(f):
                shutil.rmtree(f)
    
    print(f"[*] Downloading '{version}' ({fmt}) from {url}")
    with tempfile.NamedTemporaryFile() as of:
        res = requests.get(url)
        if res.status_code < 200 or res.status_code >= 300:
            print(f"[!] Bad result {res.status_code} {res.reason}")
            print(res.content)
            raise ValueError(f"error downloading '{url}': {res.status_code}")
        with open(of.name, "wb") as f:
            f.write(res.content)
        # Unzip the contents into the directory
        # Determine if all the files are stored in a sub-directory, if they are
        # move them out of the sub-dir
        with tempfile.TemporaryDirectory() as td:
            shutil.unpack_archive(of.name, td, fmt)
            src = td
            # Check if there is just a folder inside
            subs = os.listdir(td)
            if len(subs) == 1:
                src = os.path.join(src, subs[0])
            shutil.copytree(src, dest, dirs_exist_ok=True)
    
    # Make a commit here
    commit(dest, url, version)
    return