from abc import ABC, abstractmethod
import dagger

class DistroDriver(ABC):
    def __init__(self, image: str):
        self.image = image

    @abstractmethod
    def setup(self, container: dagger.Container) -> dagger.Container:
        """
        Perform initial setup for the distro (e.g., enabling repos, installing base build tools).
        """
        pass

    @abstractmethod
    def install_build_deps(self, container: dagger.Container, package: str) -> dagger.Container:
        """
        Install build dependencies for the specific package and fetch the source.
        """
        pass

    @abstractmethod
    def prepare_source(self, container: dagger.Container, package: str) -> dagger.Container:
        """
        Prepare the source code (e.g. unpacking source RPMs).
        """
        pass

    @abstractmethod
    async def apply_patch(self, container: dagger.Container, patch_content: str, package: str) -> dagger.Container:
        """
        Apply the patch to the source code.
        """
        pass

    @abstractmethod
    def build(self, container: dagger.Container, package: str) -> dagger.Container:
        """
        Execute the build command.
        """
        pass

    @abstractmethod
    def get_artifact_pattern(self, package: str) -> str:
        """
        Return the glob pattern to find built artifacts.
        """
        pass
    
    @abstractmethod
    def get_artifact_dir(self) -> str:
        """
        Return the directory where artifacts are built.
        """
        pass
