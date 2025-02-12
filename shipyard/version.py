import re

def _to_version_list(version) -> list:
    """Convert a version string into a list"""
    if isinstance(version, int):
        return [version]
    if not isinstance(version, str):
        version = str(version)
    return [int(i) for i in re.split(r"[^\d]", version) if i.isnumeric()]

class Version(str):
    """
    Version is a custom string that allows us to easily compare against other version strings

    Fortunately, python lists are compared in lexographical order doing exactly what we want already
    https://stackoverflow.com/a/37287545
    """
    def __init__(self, s) -> None:
        super().__init__()
        self._v = _to_version_list(s)
    
    def __hash__(self) -> int:
        return super().__hash__()
    
    def __lt__(self, other: str):
        return self._v < _to_version_list(other)
    
    def __gt__(self, other: str):
        return self._v > _to_version_list(other)

    def __eq__(self, other):
        return self._v == _to_version_list(other)

    def __le__(self, other: str) -> bool:
        return self._v <= _to_version_list(other)
    
    def __ge__(self, other: str) -> bool:
        return self._v >= _to_version_list(other)

if __name__ == '__main__':
    v = Version("openssh-7.9p1")
    assert v == "7.9.1"
    assert v < "7.10.1"
    assert v < "blah8.0"
    assert v > "7.8.1"
    assert v > "6.100.100"

    assert v >= "6.1.1"
    assert v >= "7.9.1"
    assert v <= "7.9.1"
    assert v <= "8"

    assert v < 8
    assert v < 8.0
    assert v > 7.8
    assert v > 6
    assert Version("openssh-7.9") == 7.9