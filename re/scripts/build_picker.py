"""Builds the server picker's compiled bytes from its ActionScript source, or checks the committed ones.

The picker (client_ui/serverpicker/ServerPicker.as) runs on the game's login screen in Start-Client's picker mode.
Its compiled form is committed (as hex text, 32 bytes a line), so the release and the setup need no compiler:
    client_ui/serverpicker/ServerPicker.abc.hex                the class as an ABC (ActionScript bytecode) file
    server/internal/setup/moviepatch/picker_tags.hex       the four SWF tags the setup adds to the login movie:
        DefineSprite  character 2000, one empty frame
        DoABC         the ABC above (lazy initialization, name "serverpicker")
        SymbolClass   2000 -> ServerPicker
        PlaceObject3  character 2000 at depth 2000 on the root timeline, no instance name (the movie's document
                      class is sealed: an instance name would make the player set a property it doesn't have)
re/scripts/patch_login_movie.py (and its Go port, server/internal/setup/moviepatch) inserts the tags.

Compiling needs the Apache Flex SDK's mxmlc (4.16.1), a playerglobal.swc for Flash Player 11.1 and Java: by default
tools/flex_sdk_4.16.1, tools/ffdec_26.3.0/flashlib/playerglobal32_0.swc and tools/jdk-21.0.12.1+1 of this checkout (or
of a folder above it), else the environment variables FLEX_SDK, PLAYERGLOBAL and JAVA. mxmlc writes the same ABC for
the same source, so `check` can compare byte for byte.

Usage:
    python -I re/scripts/build_picker.py build     # compile and write both files
    python -I re/scripts/build_picker.py check     # compile into a temporary folder; exit 1 if a file differs
    python -I re/scripts/build_picker.py tags <file.abc> <out.hex>   # only the tags from an ABC file
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
SOURCE_DIR = os.path.join(REPO, "client_ui", "serverpicker")
SOURCE = os.path.join(SOURCE_DIR, "ServerPicker.as")
ABC_OUT = os.path.join(SOURCE_DIR, "ServerPicker.abc.hex")
TAGS_OUT = os.path.join(REPO, "server", "internal", "setup", "moviepatch", "picker_tags.hex")
CHAR_ID = 2000
DEPTH = 2000
CLASS_NAME = "ServerPicker"
ABC_NAME = "serverpicker"


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


def compile_swf(out_swf):
    java = find_tool("JAVA", "jdk-21.0.12.1+1", "bin", "java.exe" if os.name == "nt" else "java")
    if os.path.isdir(java):
        java = os.path.join(java, "bin", "java.exe" if os.name == "nt" else "java")
    flex = find_tool("FLEX_SDK", "flex_sdk_4.16.1")
    pg = find_tool("PLAYERGLOBAL", "ffdec_26.3.0", "flashlib", "playerglobal32_0.swc")
    cmd = [java, "-Xmx512m", "-Dsun.io.useCanonCaches=false", "-Djava.util.Arrays.useLegacyMergeSort=true",
           "-jar", os.path.join(flex, "lib", "mxmlc.jar"), "+flexlib=" + os.path.join(flex, "frameworks"),
           "-load-config=", "-external-library-path=" + pg, "-source-path=" + SOURCE_DIR,
           "-target-player=11.1", "-swf-version=13", "-debug=false", "-optimize=true", "-default-size=1,1",
           "-output=" + out_swf, SOURCE]
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


def picker_tags(abc):
    sprite = tag(39, struct.pack("<HH", CHAR_ID, 1) + tag(1, b"") + tag(0, b""))
    doabc = tag(82, struct.pack("<I", 1) + ABC_NAME.encode() + b"\0" + abc)
    symbols = tag(76, struct.pack("<HH", 1, CHAR_ID) + CLASS_NAME.encode() + b"\0")
    place = tag(70, bytes([0x06, 0x00]) + struct.pack("<HH", DEPTH, CHAR_ID) + b"\0")  # HasCharacter|HasMatrix
    return sprite + doabc + symbols + place


def compile_all(out_dir):
    swf = os.path.join(out_dir, "ServerPicker.swf")
    compile_swf(swf)
    abc = swf_abc(swf)
    return abc, picker_tags(abc)


def to_hex(data):
    """The committed form of the bytes: lower-case hex, 32 bytes a line, LF (text, so the repository holds no binary)."""
    return "".join(data[i:i + 32].hex() + "\n" for i in range(0, len(data), 32)).encode("ascii")


def read(path):
    try:
        with open(path, "rb") as f:
            return f.read()
    except OSError:
        return None


def main(argv):
    args = argv[1:]
    if args == ["build"]:
        tmp = tempfile.mkdtemp(prefix="build_picker_")
        try:
            abc, tags = compile_all(tmp)
        finally:
            shutil.rmtree(tmp, ignore_errors=True)
        for path, data in ((ABC_OUT, abc), (TAGS_OUT, tags)):
            with open(path, "wb") as f:
                f.write(to_hex(data))
            print(f"{os.path.relpath(path, REPO)}: {len(data)} bytes (as hex)")
        return 0
    if args == ["check"]:
        tmp = tempfile.mkdtemp(prefix="build_picker_")
        try:
            abc, tags = compile_all(tmp)
        finally:
            shutil.rmtree(tmp, ignore_errors=True)
        bad = 0
        for path, data in ((ABC_OUT, abc), (TAGS_OUT, tags)):
            try:
                same = bytes.fromhex(read(path).decode("ascii")) == data  # whatever the line ends
            except (AttributeError, ValueError):  # missing or not hex
                same = False
            bad += not same
            print(f"{os.path.relpath(path, REPO)}: {'the source compiles to these bytes' if same else 'DIFFERS from the source (run build)'}")
        return 1 if bad else 0
    if len(args) == 3 and args[0] == "tags":
        with open(args[1], "rb") as f:
            data = picker_tags(f.read())
        with open(args[2], "wb") as f:
            f.write(to_hex(data))
        print(f"{args[2]}: {len(data)} bytes")
        return 0
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
