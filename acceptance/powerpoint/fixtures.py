"""Build deliberately-broken .pptx files from a good one. Runs anywhere.

A test that only ever opens valid files cannot tell "PowerPoint accepted it"
from "the harness never looked". These give it a known-bad input, derived from
the real compiler output so the *only* difference is the defect being probed.

Pure stdlib, no Windows, no PowerPoint -- which is why this module is unit
tested on the build host rather than only inside the VM.
"""

from __future__ import annotations

import os
import shutil
import struct
import zipfile

CONTENT_TYPES = "[Content_Types].xml"

# Offsets into the two ZIP header records, from APPNOTE.TXT.
_LOCAL_SIG = b"PK\x03\x04"
_CD_SIG = b"PK\x01\x02"
_EOCD_SIG = b"PK\x05\x06"
_LOCAL_VERSION_NEEDED = 4  # 2 bytes
_CD_VERSION_MADE_BY = 4  # 2 bytes
_CD_VERSION_NEEDED = 6  # 2 bytes


def without_content_types(src: str, dst: str) -> str:
    """Copy `src` minus its [Content_Types].xml part.

    The result is a structurally valid ZIP that is not an OPC package at all:
    [Content_Types].xml is what maps every other part to a media type, so no
    conformant reader can interpret the package without it. That makes this the
    negative control with the least room for argument -- a reader that opens it
    is not validating anything.
    """
    with zipfile.ZipFile(src) as zin, zipfile.ZipFile(dst, "w", zipfile.ZIP_DEFLATED) as zout:
        dropped = False
        for info in zin.infolist():
            if info.filename == CONTENT_TYPES:
                dropped = True
                continue
            zout.writestr(info, zin.read(info.filename))
        if not dropped:
            raise ValueError("%s has no %s to remove" % (src, CONTENT_TYPES))
    return dst


def with_zero_zip_versions(src: str, dst: str) -> str:
    """Copy `src` with every declared ZIP version field set back to 0.0.

    This reproduces, byte for byte, the defect this harness was built after:
    `archive/zip`'s CreateRaw writes whatever header it is handed, and the Go
    zero value for "version needed to extract" is 0 -- not a lenient value, not
    an old one, but not a version at all. Deflate requires 2.0.

    Every portable tool to hand (zipfile, unzip, python-pptx, LibreOffice, and
    current PowerPoint) ignores this field and infers capability from the data,
    which is exactly why a file carrying it shipped. Whether a given PowerPoint
    rejects it is therefore a *property of that PowerPoint*, so the test built
    on this fixture reports rather than asserts.
    """
    shutil.copyfile(src, dst)
    with open(dst, "r+b") as fh:
        raw = bytearray(fh.read())

        eocd = raw.rfind(_EOCD_SIG)
        if eocd < 0:
            raise ValueError("%s has no end-of-central-directory record" % src)
        count = struct.unpack_from("<H", raw, eocd + 10)[0]
        offset = struct.unpack_from("<I", raw, eocd + 16)[0]

        for _ in range(count):
            if bytes(raw[offset:offset + 4]) != _CD_SIG:
                raise ValueError("central directory is not where the EOCD says")
            struct.pack_into("<H", raw, offset + _CD_VERSION_MADE_BY, 0)
            struct.pack_into("<H", raw, offset + _CD_VERSION_NEEDED, 0)
            local = struct.unpack_from("<I", raw, offset + 42)[0]
            if bytes(raw[local:local + 4]) != _LOCAL_SIG:
                raise ValueError("local header is not where the central directory says")
            struct.pack_into("<H", raw, local + _LOCAL_VERSION_NEEDED, 0)
            name_len, extra_len, comment_len = struct.unpack_from("<HHH", raw, offset + 28)
            offset += 46 + name_len + extra_len + comment_len

        fh.seek(0)
        fh.write(raw)
        fh.truncate()
    return dst


def declared_versions(path: str) -> dict:
    """Every distinct declared version in `path`, for assertions and reports."""
    with open(path, "rb") as fh:
        raw = fh.read()

    eocd = raw.rfind(_EOCD_SIG)
    count = struct.unpack_from("<H", raw, eocd + 10)[0]
    offset = struct.unpack_from("<I", raw, eocd + 16)[0]

    local_needed: set[int] = set()
    cd_made: set[int] = set()
    cd_needed: set[int] = set()
    for _ in range(count):
        cd_made.add(struct.unpack_from("<H", raw, offset + _CD_VERSION_MADE_BY)[0])
        cd_needed.add(struct.unpack_from("<H", raw, offset + _CD_VERSION_NEEDED)[0])
        local = struct.unpack_from("<I", raw, offset + 42)[0]
        local_needed.add(struct.unpack_from("<H", raw, local + _LOCAL_VERSION_NEEDED)[0])
        name_len, extra_len, comment_len = struct.unpack_from("<HHH", raw, offset + 28)
        offset += 46 + name_len + extra_len + comment_len

    return {
        "entries": count,
        "local_version_needed": sorted(local_needed),
        "cd_version_made_by": sorted(cd_made),
        "cd_version_needed": sorted(cd_needed),
    }


def truncated(src: str, dst: str, keep_fraction: float = 0.6) -> str:
    """Copy the first `keep_fraction` of `src`. Not a ZIP at all any more."""
    size = os.path.getsize(src)
    with open(src, "rb") as fin, open(dst, "wb") as fout:
        fout.write(fin.read(int(size * keep_fraction)))
    return dst
