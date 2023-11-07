VERSION 0.7
FROM busybox

#####
# Debian base systems
#####

# All debian-based systems will use this as the base image. Does not install anything specific to the package being built,
# just generic tooling. Needs built once per source
deb-setup:
    ARG --required SOURCE
    FROM $SOURCE
    # Enable source repos and update
    RUN sed -i 's/# deb-src/deb-src/' /etc/apt/sources.list && \
        apt-get update && \
        ln -fs /usr/share/zoneinfo/America/New_York /etc/localtime && \
        # Install all the build tools. Avoid timezone prompt.
        DEBIAN_FRONTEND=noninteractive apt-get install -y gcc devscripts quilt build-essential vim iproute2 python3-pip netcat && \
        # Lots of packages that are commonly used for building
        apt-get build-dep -y openssh-server && \
        mkdir -p /tmp/build/

# Install the deps for a specific package
deb-deps:
    FROM +deb-setup
    ARG --required BUILD_PACKAGE
    WORKDIR /tmp/build
    RUN apt-get build-dep -y $BUILD_PACKAGE && \
        apt-get install -y $BUILD_PACKAGE && \
        apt-get source $BUILD_PACKAGE


#####
# Redhat based systems go here
#####
rhel-setup:
    ARG --required SOURCE
    FROM $SOURCE
    # TODO: Package installs here

rhel-deps:
    FROM +rhel-setup
    ARG --required BUILD_PACKAGE
    # TODO: Package installs here
    RUN echo "Not Implemented" && exit 127


#####
# Arch
#####
arch-setup:
    ARG --required SOURCE
    FROM $SOURCE
    RUN sed -i 's:#ParallelDownloads:ParallelDownloads:' /etc/pacman.conf && \
        pacman -Syu --noconfirm python python-pip base-devel
    # TODO: Package installs here

arch-deps:
    FROM +arch-setup
    ARG --required BUILD_PACKAGE
    # TODO: Package installs here
    RUN echo "Not Implemented" && exit 127

####
# Generic build function. Uses shipyard under the hood to build patches
####
build:
    # Docker file to build on
    ARG --required SOURCE
    # Package that we want to build against
    ARG --required BUILD_PACKAGE
    # Set to "true" to save the image. Useful for debugging
    ARG EXPORT = ""
    # If Specified, use this shipfile for generating patches
    ARG SHIPFILE
    # Patchfile that we want to use
    ARG PATCHFILE = /tmp/build/$BUILD_PACKAGE.patch
    
    IF [[ "$EXPORT" =~ "([Tt]rue|[Yy]es)" ]]
        ARG IMAGE_NAME = $BUILD_PACKAGE-builder:latest
    END
    IF [[ "$SOURCE" =~ "(debian:|ubuntu:)" ]]
        FROM +deb-deps
        ENV BUILD_MODE=deb
    ELSE IF [[ "$SOURCE" =~ "(centos:|rocky:|fedora:|amazonlinux:)" ]]
        FROM +rhel-deps
        ENV BUILD_MODE=rpm
    ELSE IF [[ "$SOURCE" =~ "(archlinux:)" ]]
        FROM +arch-setup
        ENV BUILD_MODE=arch
    ELSE
        RUN echo "Unsupported Docker image provided. You may need to modify this Earthfile 👀" && exit 127
    END

    # Install shipyard for us to use
    RUN pip install git+https://github.com/micahjmartin/Shipyard@develop

    COPY build.py /tmp/builder
    IF [ "$PATCHFILE" != "/tmp/build/$BUILD_PACKAGE.patch" ]
        COPY $PATCHFILE "/tmp/build/$BUILD_PACKAGE.patch"
    # If we are given a shipfile and not a Patchfile, generate a patch using shipyard
    ELSE IF [ "$SHIPFILE" != "" ]
        # Shipfile should be a directory with a shipfile and patches
        COPY $SHIPFILE /tmp/shipyard

        # Save here just incase shipyard errors
        IF [ "$IMAGE_NAME" != "" ]
            SAVE IMAGE $IMAGE_NAME
        END
        RUN python3 /tmp/builder gen /tmp/shipyard $PATCHFILE --package $BUILD_PACKAGE
    ELSE
        RUN echo "--PATCHFILE or --SHIPFILE must be passed to Earthly"
    END

    # Now apply the patches
    RUN python3 /tmp/builder apply $PATCHFILE --package $BUILD_PACKAGE

    # Resave the image with the new settings
    IF [ "$IMAGE_NAME" != "" ]
        SAVE IMAGE $IMAGE_NAME
    END

    #RUN python3 /tmp/builder build $BUILD_PACKAGE
