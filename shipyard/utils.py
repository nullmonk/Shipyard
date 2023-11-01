import re
import sys
import inspect

from os import path

def _load_object(fil):
    """
    Load the class object in the .py file associated with the software.
    See openssh.py for example

    We are looking for a single object that has the URL field
    TODO: We can do better, I dont want to force the project files
    to have an import though... hmm
    """
    directory, f = path.split(path.abspath(fil))
    sys.path.insert(0, directory)
    module = __import__(f[:-len(".py")])
    for name, m in inspect.getmembers(module, predicate=inspect.isclass):
        if name == "Shipfile":
            return m


def getClosestVersions(version: str, versions: list[str]) -> str:
    """Get the versions closest to the passed in version, in order"""
    vs = set(versions)
    vs.add(version)
    vs = sorted(vs)
    idx = vs.index(version)
    newVersions = []
    l, r = idx-1, idx+1
    while l >= 0 or r < len(vs):
        if l >= 0:
            nxt = vs[l]
            if nxt != version: newVersions.append(nxt)
            l-=1
        if r < len(vs):
            nxt = vs[r]
            if nxt != version: newVersions.append(nxt)
            r+=1
    return newVersions



def getMajor(s) -> int:
    """Get the major version from a string (first number in the string)"""
    vs = [int(i) for i in re.split("[^\d]+", s) if i and i.isnumeric()]
    if vs:
        return vs[0]
    return 0
