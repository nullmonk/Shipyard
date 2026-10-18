from shipyard import Builder, From, Run, Variable, Write, Copy, Workdir, Save

import re

def validate_modules(mod_name):
    # Split on commas, spaces, or semicolons
    names = [m.strip() for m in re.split(r'[,;\s]+', mod_name) if m.strip()]
    if not names:
        return ["puff"]
    return names

class Shipfile(Builder):
    Name = "puff"

    Variables = [
        Variable("NAME", "Module name(s) to build, space separated", "puff"),
        Variable("USER", "Secret username", "spidey"),
        Variable("PASSWORD", "Secret password", "red"),
        Variable("ENV", "LPE environment variable", "HISTORY_SIZE"),
        Variable("DEBUG", "Debug mode flag (set non-empty to enable)", ""),
    ]

    def build_command(self, mod_name):
        defines = "-DDEBUG" if self.ctx.variables.get("DEBUG") else ""
        cflags = "-Wno-unused-parameter -Wall -Wextra -DMOD_NAME={} {}".format(mod_name, defines).strip()
        opts = "-Wl,--export-dynamic -shared -fPIC"
        output = "libnss_{}.so.2".format(mod_name)
        Run("gcc {} -I. -o {} {} config.h *.c -Wl,-soname,{}".format(cflags, output, opts, output))
        Run("cp {} /lib/".format(output))
        return output

    def build(self):
        From("ubuntu:20.04")
        Run("apt update -y && apt-get install -y --no-install-recommends vim git strace gcc make libc6-dev libcrypt-dev openssl ca-certificates")
        Workdir("/opt")
        Copy(".")

        d = self.ctx.variables
        pw = Run("openssl passwd -1 -salt ZHJhZ29uCg -stdin", stdin=d["PASSWORD"]).stdout.strip()

        config_h = (
            '#define SECRET_USER     "{}"\n'
            '#define SECRET_PASSWORD "{}"\n'
            '#define LPE_ENV         "{}"\n'
        ).format(d["USER"], pw, d["ENV"])
        Write("/opt/config.h", config_h)

        mod_names = validate_modules(d["NAME"])
        for mod_name in mod_names:
            self.build_command(mod_name)

        Save("libnss_*.so.2", "assets/")

