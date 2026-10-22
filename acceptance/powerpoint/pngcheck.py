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

_IHDR_LEN = 13
_CHUNK_HEADER_LEN = 8
_CRC_LEN = 4


class _Malformed(Exception):
    """Internal: carries the one-line reason a file is not a usable PNG."""


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


def _load(path: str) -> bytes:
    if not os.path.isfile(path):
        raise _Malformed("not written")
    if os.path.getsize(path) == 0:
        raise _Malformed("zero bytes")
    with open(path, "rb") as fh:
        raw = fh.read()
    if not raw.startswith(SIGNATURE):
        raise _Malformed("no PNG signature (first 8 bytes: %s)" % raw[:8].hex())
    return raw


def _chunks(raw: bytes) -> list[tuple[bytes, bytes]]:
    """Every (type, data) pair, with each chunk's CRC verified."""
    out: list[tuple[bytes, bytes]] = []
    offset = len(SIGNATURE)
    while offset < len(raw):
        if offset + _CHUNK_HEADER_LEN > len(raw):
            raise _Malformed("truncated chunk header at byte %d" % offset)
        length = int.from_bytes(raw[offset:offset + 4], "big")
        ctype = raw[offset + 4:offset + 8]
        body = offset + _CHUNK_HEADER_LEN
        end = body + length
        if end + _CRC_LEN > len(raw):
            raise _Malformed(
                "chunk %r claims %d bytes but the file ends at %d"
                % (ctype.decode("latin-1"), length, len(raw))
            )
        data = raw[body:end]
        declared = int.from_bytes(raw[end:end + _CRC_LEN], "big")
        actual = binascii.crc32(ctype + data) & 0xFFFFFFFF
        if declared != actual:
            raise _Malformed(
                "chunk %r CRC mismatch (declared %08x, computed %08x)"
                % (ctype.decode("latin-1"), declared, actual)
            )
        out.append((ctype, data))
        offset = end + _CRC_LEN
    return out


def _header(data: bytes) -> Png:
    if len(data) != _IHDR_LEN:
        raise _Malformed("IHDR is %d bytes, must be %d" % (len(data), _IHDR_LEN))
    width = int.from_bytes(data[0:4], "big")
    height = int.from_bytes(data[4:8], "big")
    if width == 0 or height == 0:
        raise _Malformed("degenerate size %dx%d" % (width, height))
    if data[9] not in _CHANNELS:
        raise _Malformed("unknown colour type %d" % data[9])
    return Png(
        ok=True,
        width=width,
        height=height,
        bit_depth=data[8],
        colour_type=data[9],
        interlaced=data[12] != 0,
    )


def _expected_pixel_bytes(header: Png) -> int:
    channels = _CHANNELS[header.colour_type]
    row = 1 + math.ceil(header.width * channels * header.bit_depth / 8)
    return row * header.height


def _assemble(chunks: list[tuple[bytes, bytes]]) -> Png:
    header: Png | None = None
    idat = bytearray()
    saw_iend = False

    for index, (ctype, data) in enumerate(chunks):
        if ctype == b"IHDR":
            if header is not None:
                raise _Malformed("more than one IHDR")
            header = _header(data)
        elif index == 0:
            raise _Malformed("first chunk is %r, must be IHDR" % ctype.decode("latin-1"))
        elif ctype == b"IDAT":
            idat += data
        elif ctype == b"IEND":
            saw_iend = True

    if header is None:
        raise _Malformed("no IHDR")
    if not saw_iend:
        raise _Malformed("no IEND -- the file is truncated")
    if not idat:
        raise _Malformed("no image data (no IDAT chunk)")

    try:
        pixels = zlib.decompress(bytes(idat))
    except zlib.error as exc:
        raise _Malformed("image data will not decompress (%s)" % exc) from exc

    header.idat_bytes = len(idat)

    # Adam7's seven passes each have their own row padding, so the simple
    # product does not apply; the CRC and zlib checks above already caught
    # truncation, so accept rather than guess.
    if not header.interlaced:
        expected = _expected_pixel_bytes(header)
        if len(pixels) != expected:
            raise _Malformed(
                "image data is %d bytes, IHDR implies %d (%dx%d) -- incomplete render"
                % (len(pixels), expected, header.width, header.height)
            )
    return header


def inspect(path: str) -> Png:
    """Structurally validate the PNG at `path`."""
    try:
        return _assemble(_chunks(_load(path)))
    except _Malformed as exc:
        return Png(ok=False, problem=str(exc))
