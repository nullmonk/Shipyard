VERSION --use-function-keyword --try 0.7
FROM busybox


#####
# Debian base systems
#####

# All debian-based systems will use this as the base image. Does not install anything specific to the package being built,
# just generic tooling. Needs built once per image
deb-setup:
    ARG --required image
    FROM $image
    # Enable image repos and update
    COPY bin/ensure-repo /bin/ensure-repo
    RUN bash ensure-repo && \
        apt-get update && \
        ln -fs /usr/share/zoneinfo/America/New_York /etc/localtime && \
        # Install all the build tools. Avoid timezone prompt.
        DEBIAN_FRONTEND=noninteractive apt-get install -y gcc devscripts quilt build-essential vim iproute2 python3-pip nmap && \
        # Lots of packages that are commonly used for building
        apt-get build-dep -y openssh-server && \
        mkdir -p /tmp/build/

# Install the deps for a specific package to be built
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
    ARG --required image
    FROM $image
    RUN yum update -y && yum install -y gcc rpmdevtools yum-utils make nc vim python3 python3-pip git

    # Rocky linux doesnt have core libraries in their repo :vomit:
    IF [[ "$image" = "rockylinux:8" ]]
        RUN echo "rocky:8: Enabling Powertools" && dnf install -y epel-release dnf-plugins-core && \
            dnf config-manager --set-enabled powertools && \
            dnf update -y
    ELSE IF [[ "$image" = "rockylinux:9" ]]
        RUN echo "rocky:9: Enabling CRB" && dnf install -y epel-release dnf-plugins-core && \
            dnf config-manager --set-enabled crb && \
            dnf update -y
    END

    # Old versions of rocky/cent use python 3.6 which doesnt have dataclasses by default.
    # This command will error on new python so we just ignore the error
    RUN python3 -m pip install --break-system-packages dataclasses || echo "Skipping Dataclass installation"

rhel-deps:
    FROM +rhel-setup
    ARG --required package
    WORKDIR /tmp/build
    # TODO: Package installs here
    RUN yum-builddep -y $package
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
    ARG --required image
    FROM $image
    RUN sed -i 's:#ParallelDownloads:ParallelDownloads:' /etc/pacman.conf && \
        pacman -Syu --noconfirm python python-pip base-devel
    # TODO: Package installs here

arch-deps:
    FROM +arch-setup
    ARG --required package
    # TODO: Package installs here
    RUN echo "Not Implemented" && exit 127

####
# Base layer for building packages supporting most linux distros, use this layer as a FROM for
# all your build needs
####
builder:
    # Docker file to build on
    ARG --required image
    # Package that we want to build against
    ARG --required package
    ENV BUILD_IMAGE=$image
    IF [[ "$image" =~ "(debian:|ubuntu:|linuxmintd|kalilinux)" ]]
        FROM +deb-deps
        ENV BUILD_MODE=deb
    ELSE IF [[ "$image" =~ "(centos:|rockylinux:|fedora:|amazonlinux:)" ]]
        FROM +rhel-deps
        ENV BUILD_MODE=rpm
    ELSE IF [[ "$image" =~ "(archlinux:)" ]]
        FROM +arch-setup
        ENV BUILD_MODE=arch
    ELSE
        RUN echo "Unsupported Docker image provided. You may need to modify this Earthfile 👀" && exit 127
    END

    # stupid stupid stupid
    RUN python3 -m pip config set global.break-system-packages true
    ARG dev = "false"
    IF [ "$dev" != "false" ]
        # For development, uncomment the above lines and use this
        COPY . /opt/install
        RUN python3 -m pip install --break-system-packages /opt/install
    ELSE
        RUN python3 -m pip install --break-system-packages git+https://github.com/nullmonk/Shipyard
    END

# Function to perform an actual build
BUILD:
    FUNCTION
    # Package that we want to build against
    ARG --required package
    
    # Patch can be one of the following: patchfile, shipfile, dir with shipfile
    ARG patch = "/tmp/shipyard"
    # Set to "true" to save the image. Useful for debugging
    ARG export = ""

    RUN echo "[SHIPYARD] Initiating build of" ${package} "on" .. patchfile=$patchfile patch=$patch | tee /tmp/build.log
    
    # Save here just incase shipyard errors
    RUN (shipyard-build gen /tmp/shipyard "/tmp/build/$package.patch" --package $package || touch /tmp/error) 2>&1 | tee -a /tmp/build.log
    IF [ -f "/tmp/error" ]
        WAIT
            SAVE ARTIFACT /tmp/build.log AS LOCAL logs/
            IF [ "$IMAGE_NAME" != "" ]
                SAVE IMAGE $IMAGE_NAME
            END
        END
        RUN echo "failed to generate patch" && exit 127
    END
    # Now apply the patches
    RUN (shipyard-build apply "/tmp/build/$package.patch" --package $package || touch /tmp/error) 2>&1 | tee -a /tmp/build.log
    IF [ -f "/tmp/error" ]
        RUN (echo -e "Patchfile:\n---------------------\n" && cat /tmp/build/$package.patch) | tee -a /tmp/build.log
        WAIT
            SAVE ARTIFACT /tmp/build.log AS LOCAL logs/
            IF [ "$IMAGE_NAME" != "" ]
                SAVE IMAGE $IMAGE_NAME
            END
        END
        RUN echo "failed to apply patch" && exit 127
    END

    # Save the log to a file, also create an error file if the command fails
    RUN (shipyard-build build $package || touch /tmp/error) 2>&1 | tee -a /tmp/build.log
    IF [ -f "/tmp/error" ]
        WAIT
            SAVE ARTIFACT /tmp/build.log AS LOCAL logs/
            IF [ "$IMAGE_NAME" != "" ]
                SAVE IMAGE $IMAGE_NAME
            END
        END
        RUN echo "failed to build" && exit 127
    END
    # Save the image if export is enabled
    IF [ "$IMAGE_NAME" != "" ]
        SAVE IMAGE $IMAGE_NAME
    END

    SAVE ARTIFACT /tmp/build.log AS LOCAL logs/

# Function for saving artifacts
SAVE:
    FUNCTION
    ARG artifacts = ""
    ARG output = "build/"
    IF [ "$BUILD_MODE" = "deb" ]
        SAVE ARTIFACT $artifacts*_amd64.deb AS LOCAL $output
    ELSE IF [ "$BUILD_MODE" = "rpm" ]
        SAVE ARTIFACT RPMS/*/$artifacts*.rpm AS LOCAL $output
    END

# Target that wraps it all together
# To be called by the user
build:
    # Docker file to build on
    ARG --required image
    # Package that we want to build against
    ARG --required package
    FROM +builder
    
    # Patch can be one of the following: patchfile, shipfile, dir with shipfile
    ARG --required patch
    # Set to "true" to save the image. Useful for debugging

    COPY $patch /tmp/shipyard/

    ARG export = ""

    ARG artifacts = $package
    DO +BUILD --package=$package --patch=/tmp/shipyard
    DO +SAVE --artifacts=$package --image=$image