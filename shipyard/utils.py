import re
import sys
import inspect

from os import path
from typing import List

from shipyard.version import Version

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


def getClosestVersions(version: str, versions: List[Version]) -> str:
    """Get the versions closest to the passed in version, in order"""
    vs = set(versions)
    vs.add(version)
    vs = sorted(vs)
    idx = vs.index(version)
    newVersions = []
    left, right = idx-1, idx+1
    while left >= 0 or right < len(vs):
        if left >= 0:
            nxt = vs[left]
            if nxt != version:
                newVersions.append(nxt)
            left-=1
        if right < len(vs):
            nxt = vs[right]
            if nxt != version:
                newVersions.append(nxt)
            right+=1
    if not newVersions:
        return [version]
    return newVersions
