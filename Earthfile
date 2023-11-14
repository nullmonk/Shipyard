VERSION 0.7
FROM busybox

#####
# Debian base systems
#####

# All debian-based systems will use this as the base image. Does not install anything specific to the package being built,
# just generic tooling. Needs built once per source
deb-setup:
    ARG --required source
    FROM $source
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
    ARG --required package
    WORKDIR /tmp/build
    RUN apt-get build-dep -y $package && \
        apt-get install -y $package && \
        apt-get source $package


#####
# Redhat based systems go here
#####
rhel-setup:
    ARG --required source
    FROM $source
    RUN yum update -y && yum install -y gcc rpmdevtools yum-utils make nc vim python3 python3-pip git

rhel-deps:
    FROM +rhel-setup
    ARG --required package
    WORKDIR /tmp/build
    # TODO: Package installs here
    RUN yum-builddep -y $package && \
        python3 -m pip install dataclasses
    RUN useradd -m mockbuild && groupadd mock && \
        usermod -G wheel mockbuild && \
        echo "%wheel  ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers && \
        echo "root:toor" | chpasswd && rpmdev-setuptree && \
        echo '%_topdir      /tmp/build' > ~/.rpmmacros && \
        yumdownloader --source $package && \
        rpm -ivh *.src.rpm && \
        rpmbuild -bp SPECS/*.spec


#####
# Arch
#####
arch-setup:
    ARG --required source
    FROM $source
    RUN sed -i 's:#ParallelDownloads:ParallelDownloads:' /etc/pacman.conf && \
        pacman -Syu --noconfirm python python-pip base-devel
    # TODO: Package installs here

arch-deps:
    FROM +arch-setup
    ARG --required package
    # TODO: Package installs here
    RUN echo "Not Implemented" && exit 127

####
# Generic build function. Uses shipyard under the hood to build patches
####
build:
    # Docker file to build on
    ARG --required source
    # Package that we want to build against
    ARG --required package
    # Set to "true" to save the image. Useful for debugging
    ARG export = ""
    # If Specified, use this shipfile for generating patches
    ARG shipfile
    # Patchfile that we want to use
    ARG patchfile
    
    ARG artifacts = $package # Artifacts to extract
    # Enfore we have ONE of the two args
    IF [ "$patchfile" = "" ] && [ "$shipfile" = "" ]
        RUN echo "--patchfile or --shipfile must be passed to Earthly" && exit 127
    END

    IF [[ "$export" =~ "([Tt]rue|[Yy]es)" ]]
        ARG IMAGE_NAME = $package-builder:latest
    END
    IF [[ "$source" =~ "(debian:|ubuntu:|linuxmintd|kalilinux)" ]]
        FROM +deb-deps
        ENV BUILD_MODE=deb
    ELSE IF [[ "$source" =~ "(centos:|rockylinux:|fedora:|amazonlinux:)" ]]
        FROM +rhel-deps
        ENV BUILD_MODE=rpm
    ELSE IF [[ "$source" =~ "(archlinux:)" ]]
        FROM +arch-setup
        ENV BUILD_MODE=arch
    ELSE
        RUN echo "Unsupported Docker image provided. You may need to modify this Earthfile 👀" && exit 127
    END

    # Install shipyard for us to use
    #RUN pip install git+https://github.com/micahjmartin/Shipyard@develop
    COPY . /opt/install
    RUN python3 -m pip install /opt/install

    COPY shipyard/build.py /tmp/builder
    RUN echo "[SHIPYARD] Initiating build of $package on $source ... patchfile=$patchfile shipfile=$shipfile" | tee /tmp/build.log
    IF [ "$patchfile" != "" ]
        COPY $patchfile "/tmp/build/$package.patch"
    # If we are given a shipfile and not a Patchfile, generate a patch using shipyard
    ELSE IF [ "$shipfile" != "" ]
        # Shipfile should be a directory with a shipfile and patches
        COPY $shipfile /tmp/shipyard/
        # Save here just incase shipyard errors
        IF [ "$IMAGE_NAME" != "" ]
            SAVE IMAGE $IMAGE_NAME
        END
        RUN python3 /tmp/builder gen /tmp/shipyard "/tmp/build/$package.patch" --package $package 2>&1 | tee -a /tmp/build.log
    END

    # Now apply the patches
    RUN python3 /tmp/builder apply "/tmp/build/$package.patch" --package $package 2>&1 | tee -a /tmp/build.log

    # Resave the image with the new settings
    IF [ "$IMAGE_NAME" != "" ]
        SAVE IMAGE $IMAGE_NAME
    END

    # Save the log to a file, also create an error file if the command fails
    RUN (python3 /tmp/builder build $package || touch /tmp/error) 2>&1 | tee -a /tmp/build.log

    IF [ -f "/tmp/build.log" ]
        SAVE ARTIFACT /tmp/build.log AS LOCAL logs/
    END

    # If the build failed, exitout
    IF [ -f "/tmp/error" ]
        RUN echo "Build failed" && exit 127
    ELSE
        RUN echo "build worked"
    END
    IF [ "$BUILD_MODE" = "deb" ]
        SAVE ARTIFACT $artifacts*_amd64.deb AS LOCAL build/deb/
    ELSE IF [ "$BUILD_MODE" = "rpm" ]
        SAVE ARTIFACT RPMS/*/$artifacts*.rpm AS LOCAL build/rpm/
    END