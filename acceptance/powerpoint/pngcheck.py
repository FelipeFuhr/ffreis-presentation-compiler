"""Decide whether a file is a complete, intact PNG. Pure stdlib, runs anywhere.

This replaced Pillow, which was carried for exactly two things: grabbing a
screenshot and checking an exported slide was readable. It brought a dozen
critical/high advisories with it (heap overflows in the TIFF and JPEG
decoders, an argument injection into Ghostscript) in exchange for decoding
formats this harness never touches, and it pinned the guest's Python version.

The replacement is also a *stronger* check than `Image.open().verify()`. That
validates chunk CRCs but does not decompress the pixel data, so it accepts a
PNG whose IDAT stream is truncated. `inspect` below decompresses the stream
and compares its length against the size IHDR implies -- which is the actual
question being asked of a slide export: did PowerPoint finish drawing it.
"""

from __future__ import annotations

import binascii
import dataclasses
import math
import os
import zlib

SIGNATURE = b"\x89PNG\r\n\x1a\n"

# Samples per pixel by PNG colour type (0 grey, 2 RGB, 3 palette, 4 grey+A,
# 6 RGBA). Needed to predict the decompressed size from IHDR.
_CHANNELS = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}


@dataclasses.dataclass
class Png:
    ok: bool
    problem: str | None = None
    width: int = 0
    height: int = 0
    bit_depth: int = 0
    colour_type: int = 0
    interlaced: bool = False
    idat_bytes: int = 0

    def describe(self) -> str:
        if self.ok:
            return "%dx%d, %d-bit colour type %d" % (
                self.width, self.height, self.bit_depth, self.colour_type,
            )
        return self.problem or "not a PNG"


def _bad(problem: str) -> Png:
    return Png(ok=False, problem=problem)


def inspect(path: str) -> Png:
    """Structurally validate the PNG at `path`."""
    if not os.path.isfile(path):
        return _bad("not written")
    size = os.path.getsize(path)
    if size == 0:
        return _bad("zero bytes")

    with open(path, "rb") as fh:
        raw = fh.read()

    if not raw.startswith(SIGNATURE):
        return _bad("no PNG signature (first 8 bytes: %s)" % raw[:8].hex())

    offset = len(SIGNATURE)
    header: Png | None = None
    idat = bytearray()
    saw_iend = False

    while offset < len(raw):
        if offset + 8 > len(raw):
            return _bad("truncated chunk header at byte %d" % offset)
        length = int.from_bytes(raw[offset:offset + 4], "big")
        ctype = raw[offset + 4:offset + 8]
        body = offset + 8
        end = body + length
        if end + 4 > len(raw):
            return _bad(
                "chunk %r claims %d bytes but the file ends at %d"
                % (ctype.decode("latin-1"), length, len(raw))
            )

        data = raw[body:end]
        declared = int.from_bytes(raw[end:end + 4], "big")
        actual = binascii.crc32(ctype + data) & 0xFFFFFFFF
        if declared != actual:
            return _bad(
                "chunk %r CRC mismatch (declared %08x, computed %08x)"
                % (ctype.decode("latin-1"), declared, actual)
            )

        if ctype == b"IHDR":
            if header is not None:
                return _bad("more than one IHDR")
            if length != 13:
                return _bad("IHDR is %d bytes, must be 13" % length)
            width = int.from_bytes(data[0:4], "big")
            height = int.from_bytes(data[4:8], "big")
            header = Png(
                ok=True,
                width=width,
                height=height,
                bit_depth=data[8],
                colour_type=data[9],
                interlaced=data[12] != 0,
            )
            if width == 0 or height == 0:
                return _bad("degenerate size %dx%d" % (width, height))
            if data[9] not in _CHANNELS:
                return _bad("unknown colour type %d" % data[9])
        elif header is None:
            return _bad("first chunk is %r, must be IHDR" % ctype.decode("latin-1"))
        elif ctype == b"IDAT":
            idat += data
        elif ctype == b"IEND":
            saw_iend = True

        offset = end + 4

    if header is None:
        return _bad("no IHDR")
    if not saw_iend:
        return _bad("no IEND -- the file is truncated")
    if not idat:
        return _bad("no image data (no IDAT chunk)")

    try:
        pixels = zlib.decompress(bytes(idat))
    except zlib.error as exc:
        return _bad("image data will not decompress (%s)" % exc)

    header.idat_bytes = len(idat)

    # Adam7's seven passes each have their own row padding, so the simple
    # product below does not apply; the CRC and zlib checks above already
    # caught truncation, so accept rather than guess.
    if not header.interlaced:
        channels = _CHANNELS[header.colour_type]
        row = 1 + math.ceil(header.width * channels * header.bit_depth / 8)
        expected = row * header.height
        if len(pixels) != expected:
            return _bad(
                "image data is %d bytes, IHDR implies %d (%dx%d) -- incomplete render"
                % (len(pixels), expected, header.width, header.height)
            )

    return header
