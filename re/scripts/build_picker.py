"""Builds the compiled bytes of our UI classes from their ActionScript source, or checks the committed ones.

Two classes run inside the game's own UI movies, each placed there by four added SWF tags (the game's bytecode stays
as it is; re/scripts/patch_login_movie.py and its Go port, server/internal/setup/moviepatch, insert them):
    picker        client_ui/serverpicker/ServerPicker.as, the server picker on the login screen (Start-Client's
                  picker mode), in UI/Scaleform/LoginScreen/LoginScreen_BG.gfx
    matchoptions  client_ui/matchoptions/BotCheckbox.as, the "Fill open seats with bots" check box of the CREATE GAME
                  dialog, in UI/Scaleform/Battlegrounds/MatchOptionsUI.gfx
Their compiled form is committed (as hex text, 32 bytes a line), so the release and the setup need no compiler:
    client_ui/<folder>/<Class>.abc.hex                 the class as an ABC (ActionScript bytecode) file
    server/internal/setup/moviepatch/<name>_tags.hex   the four SWF tags the setup adds to the movie:
        DefineSprite  character 2000, one empty frame
        DoABC         the ABC above (lazy initialization, named after the class in lower case)
        SymbolClass   2000 -> the class
        PlaceObject3  character 2000 at depth 2000 on the root timeline, no instance name (the movies' document
                      classes are sealed: an instance name would make the player set a property they don't have)

Compiling needs the Apache Flex SDK's mxmlc (4.16.1), a playerglobal.swc for Flash Player 11.1 and Java: by default
tools/flex_sdk_4.16.1, tools/ffdec_26.3.0/flashlib/playerglobal32_0.swc and tools/jdk-21.0.12.1+1 of this checkout (or
of a folder above it), else the environment variables FLEX_SDK, PLAYERGLOBAL and JAVA. mxmlc writes the same ABC for
the same source, so `check` can compare byte for byte.

Usage:
    python -I re/scripts/build_picker.py build [picker|matchoptions]   # compile and write both files (default: all)
    python -I re/scripts/build_picker.py check [picker|matchoptions]   # compile into a temporary folder; exit 1 if a
                                                                         file differs
    python -I re/scripts/build_picker.py tags <file.abc> <out.hex> [--spec picker|matchoptions]   # only the tags
    python -I re/scripts/build_picker.py compile <Source.as> <out_tags.hex>   # a throwaway class (a spike): its tags,
                                                     character 2000 at depth 2000, class = the file's name
"""
import os
import shutil
import struct
import subprocess
import sys
import tempfile
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
MOVIEPATCH = os.path.join(REPO, "server", "internal", "setup", "moviepatch")
CHAR_ID = 2000
DEPTH = 2000


class Spec:
    """One class: its source, its committed files, its character id and depth in its movie."""

    def __init__(self, folder, class_name, tags_name):
        self.source_dir = os.path.join(REPO, "client_ui", folder)
        self.class_name = class_name
        self.source = os.path.join(self.source_dir, class_name + ".as")
        self.abc_out = os.path.join(self.source_dir, class_name + ".abc.hex")
        self.tags_out = os.path.join(MOVIEPATCH, tags_name)
        self.abc_name = class_name.lower()
        self.char_id = CHAR_ID
        self.depth = DEPTH


SPECS = {
    "picker": Spec("serverpicker", "ServerPicker", "picker_tags.hex"),
    "matchoptions": Spec("matchoptions", "BotCheckbox", "matchoptions_tags.hex"),
}
# The picker's names, as before the specs (the tests and older tools read them).
SOURCE_DIR = SPECS["picker"].source_dir
SOURCE = SPECS["picker"].source
ABC_OUT = SPECS["picker"].abc_out
TAGS_OUT = SPECS["picker"].tags_out
CLASS_NAME = SPECS["picker"].class_name
ABC_NAME = SPECS["picker"].abc_name


def find_tool(env, *rel):
    if os.environ.get(env):
        return os.environ[env]
    here = REPO
    for _ in range(5):
        p = os.path.join(here, "tools", *rel)
        if os.path.exists(p):
            return p
        here = os.path.dirname(here)
    raise SystemExit(f"{os.path.join('tools', *rel)} not found: set {env} (see the docstring)")


def compile_swf(out_swf, source=SOURCE):
    java = find_tool("JAVA", "jdk-21.0.12.1+1", "bin", "java.exe" if os.name == "nt" else "java")
    if os.path.isdir(java):
        java = os.path.join(java, "bin", "java.exe" if os.name == "nt" else "java")
    flex = find_tool("FLEX_SDK", "flex_sdk_4.16.1")
    pg = find_tool("PLAYERGLOBAL", "ffdec_26.3.0", "flashlib", "playerglobal32_0.swc")
    cmd = [java, "-Xmx512m", "-Dsun.io.useCanonCaches=false", "-Djava.util.Arrays.useLegacyMergeSort=true",
           "-jar", os.path.join(flex, "lib", "mxmlc.jar"), "+flexlib=" + os.path.join(flex, "frameworks"),
           "-load-config=", "-external-library-path=" + pg, "-source-path=" + os.path.dirname(os.path.abspath(source)),
           "-target-player=11.1", "-swf-version=13", "-debug=false", "-optimize=true", "-default-size=1,1",
           "-output=" + out_swf, source]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        sys.stdout.write(r.stdout)
        sys.stderr.write(r.stderr)
        raise SystemExit(f"mxmlc failed ({r.returncode})")
    warnings = [ln for ln in (r.stdout + r.stderr).splitlines() if "Warning" in ln]
    for ln in warnings:
        print(ln)


def swf_abc(path):
    """The only DoABC tag's abcFile bytes from a SWF (FWS or CWS)."""
    with open(path, "rb") as f:
        raw = f.read()
    if raw[:3] == b"CWS":
        body = zlib.decompress(raw[8:])
    elif raw[:3] == b"FWS":
        body = raw[8:]
    else:
        raise SystemExit(f"{path}: not a SWF")
    pos = (5 + 4 * (body[0] >> 3) + 7) // 8 + 4
    found = []
    while pos < len(body):
        (h,) = struct.unpack_from("<H", body, pos)
        code, ln, hdr = h >> 6, h & 0x3F, 2
        if ln == 0x3F:
            (ln,) = struct.unpack_from("<I", body, pos + 2)
            hdr = 6
        data = body[pos + hdr:pos + hdr + ln]
        if code == 82:
            name_end = data.index(b"\0", 4)
            found.append(data[name_end + 1:])
        elif code == 72:
            found.append(data)
        pos += hdr + ln
        if code == 0:
            break
    if len(found) != 1:
        raise SystemExit(f"{path}: {len(found)} DoABC tags, expected 1")
    return found[0]


def tag(code, data):
    if len(data) < 0x3F:
        return struct.pack("<H", (code << 6) | len(data)) + data
    return struct.pack("<HI", (code << 6) | 0x3F, len(data)) + data


def class_tags(abc, class_name, abc_name, char_id=CHAR_ID, depth=DEPTH):
    """DefineSprite, DoABC, SymbolClass and PlaceObject3 (no name) for one class."""
    sprite = tag(39, struct.pack("<HH", char_id, 1) + tag(1, b"") + tag(0, b""))
    doabc = tag(82, struct.pack("<I", 1) + abc_name.encode() + b"\0" + abc)
    symbols = tag(76, struct.pack("<HH", 1, char_id) + class_name.encode() + b"\0")
    place = tag(70, bytes([0x06, 0x00]) + struct.pack("<HH", depth, char_id) + b"\0")  # HasCharacter|HasMatrix
    return sprite + doabc + symbols + place


def picker_tags(abc, spec=None):
    spec = spec or SPECS["picker"]
    return class_tags(abc, spec.class_name, spec.abc_name, spec.char_id, spec.depth)


def compile_all(out_dir, spec=None):
    spec = spec or SPECS["picker"]
    swf = os.path.join(out_dir, spec.class_name + ".swf")
    compile_swf(swf, spec.source)
    abc = swf_abc(swf)
    return abc, picker_tags(abc, spec)


def to_hex(data):
    """The committed form of the bytes: lower-case hex, 32 bytes a line, LF (text, so the repository holds no binary)."""
    return "".join(data[i:i + 32].hex() + "\n" for i in range(0, len(data), 32)).encode("ascii")


def read(path):
    try:
        with open(path, "rb") as f:
            return f.read()
    except OSError:
        return None


def specs_of(names):
    for n in names:
        if n not in SPECS:
            raise SystemExit(f"unknown class '{n}': one of {', '.join(SPECS)}")
    return [SPECS[n] for n in (names or list(SPECS))]


def compiled(spec):
    tmp = tempfile.mkdtemp(prefix="build_picker_")
    try:
        return compile_all(tmp, spec)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main(argv):
    args = argv[1:]
    if args and args[0] == "build":
        for spec in specs_of(args[1:]):
            abc, tags = compiled(spec)
            for path, data in ((spec.abc_out, abc), (spec.tags_out, tags)):
                with open(path, "wb") as f:
                    f.write(to_hex(data))
                print(f"{os.path.relpath(path, REPO)}: {len(data)} bytes (as hex)")
        return 0
    if args and args[0] == "check":
        bad = 0
        for spec in specs_of(args[1:]):
            abc, tags = compiled(spec)
            for path, data in ((spec.abc_out, abc), (spec.tags_out, tags)):
                try:
                    same = bytes.fromhex(read(path).decode("ascii")) == data  # whatever the line ends
                except (AttributeError, ValueError):  # missing or not hex
                    same = False
                bad += not same
                print(f"{os.path.relpath(path, REPO)}: {'the source compiles to these bytes' if same else 'DIFFERS from the source (run build)'}")
        return 1 if bad else 0
    if len(args) in (3, 5) and args[0] == "tags":
        spec = SPECS["picker"]
        if len(args) == 5:
            if args[3] != "--spec":
                print(__doc__)
                return 2
            spec = specs_of([args[4]])[0]
        with open(args[1], "rb") as f:
            data = picker_tags(f.read(), spec)
        with open(args[2], "wb") as f:
            f.write(to_hex(data))
        print(f"{args[2]}: {len(data)} bytes")
        return 0
    if len(args) == 3 and args[0] == "compile":
        source = os.path.abspath(args[1])
        name = os.path.splitext(os.path.basename(source))[0]
        tmp = tempfile.mkdtemp(prefix="build_picker_")
        try:
            swf = os.path.join(tmp, name + ".swf")
            compile_swf(swf, source)
            data = class_tags(swf_abc(swf), name, name.lower())
        finally:
            shutil.rmtree(tmp, ignore_errors=True)
        with open(args[2], "wb") as f:
            f.write(to_hex(data))
        print(f"{args[2]}: {len(data)} bytes ({name}, character {CHAR_ID} at depth {DEPTH})")
        return 0
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
