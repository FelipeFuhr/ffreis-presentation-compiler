"""Tests for the parts of this harness that do not need Windows.

Three of these modules are plain byte-pushing -- building a known-bad package,
validating a PNG, containing a path -- and all three are the reason a
PowerPoint result can be trusted: if `fixtures` silently stopped breaking the
negative control, or `pngcheck` stopped rejecting a half-written export, the
suite would go green while measuring nothing.

So they are tested here rather than only inside the VM. Everything is built
in-process: no compiler, no Chrome, no PowerPoint, no network.
"""

from __future__ import annotations

import os
import struct
import zipfile
import zlib

import pytest

import artifacts
import fixtures
import pngcheck

# ── helpers: minimal valid inputs, built in-process ─────────────────────────


def _png(width: int = 3, height: int = 2) -> bytes:
    """A structurally perfect 8-bit RGB PNG of the requested size."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + kind
            + data
            + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
        )

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    rows = b"".join(b"\x00" + bytes([(x * 7) % 256] * 3 * width) for x in range(height))
    return (
        pngcheck.SIGNATURE
        + chunk(b"IHDR", ihdr)
        + chunk(b"IDAT", zlib.compress(rows))
        + chunk(b"IEND", b"")
    )


def _pptx(path: str, parts: int = 4) -> str:
    """A zip shaped like an OPC package: [Content_Types].xml first, then parts."""
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
        z.writestr(fixtures.CONTENT_TYPES, '<?xml version="1.0"?><Types/>')
        for index in range(parts):
            z.writestr("ppt/slides/slide%d.xml" % (index + 1), "<sld n='%d'/>" % index)
    return path


@pytest.fixture
def good_pptx(tmp_path) -> str:
    return _pptx(str(tmp_path / "good.pptx"))


@pytest.fixture
def good_png(tmp_path) -> str:
    path = str(tmp_path / "good.png")
    with open(path, "wb") as fh:
        fh.write(_png())
    return path


# ── fixtures.py ─────────────────────────────────────────────────────────────


def test_a_good_package_declares_a_real_zip_version(good_pptx: str) -> None:
    declared = fixtures.declared_versions(good_pptx)
    assert declared["entries"] == 5
    assert declared["local_version_needed"] == [20]
    assert declared["cd_version_needed"] == [20]


def test_without_content_types_drops_only_that_part(good_pptx: str, tmp_path) -> None:
    broken = fixtures.without_content_types(good_pptx, str(tmp_path / "no-ct.pptx"))
    with zipfile.ZipFile(broken) as broken_zip, zipfile.ZipFile(good_pptx) as good_zip:
        assert fixtures.CONTENT_TYPES not in broken_zip.namelist()
        # Still a valid ZIP -- just not an OPC package. That is what makes it a
        # clean control: only the content-type map is gone.
        assert broken_zip.testzip() is None
        expected = [n for n in good_zip.namelist() if n != fixtures.CONTENT_TYPES]
        assert broken_zip.namelist() == expected
        assert all(broken_zip.read(n) == good_zip.read(n) for n in expected)


def test_without_content_types_refuses_a_package_that_has_none(tmp_path) -> None:
    bare = str(tmp_path / "bare.zip")
    with zipfile.ZipFile(bare, "w") as z:
        z.writestr("only.xml", "<x/>")
    with pytest.raises(ValueError, match=r"\[Content_Types\]"):
        fixtures.without_content_types(bare, str(tmp_path / "out.pptx"))


def test_zero_zip_versions_changes_the_headers_and_nothing_else(good_pptx: str, tmp_path) -> None:
    """The whole point of this fixture: only the advisory field differs.

    If it altered a part, a PowerPoint rejecting the result would prove
    nothing about the version field.
    """
    probe = fixtures.with_zero_zip_versions(good_pptx, str(tmp_path / "zero.pptx"))
    declared = fixtures.declared_versions(probe)
    assert declared["local_version_needed"] == [0]
    assert declared["cd_version_made_by"] == [0]
    assert declared["cd_version_needed"] == [0]

    with zipfile.ZipFile(probe) as probe_zip, zipfile.ZipFile(good_pptx) as good_zip:
        assert probe_zip.testzip() is None
        assert probe_zip.namelist() == good_zip.namelist()
        assert all(probe_zip.read(n) == good_zip.read(n) for n in good_zip.namelist())


def test_truncated_is_no_longer_a_zip(good_pptx: str, tmp_path) -> None:
    cut = fixtures.truncated(good_pptx, str(tmp_path / "cut.pptx"))
    assert os.path.getsize(cut) < os.path.getsize(good_pptx)
    with pytest.raises(zipfile.BadZipFile):
        zipfile.ZipFile(cut).namelist()


# ── pngcheck.py ─────────────────────────────────────────────────────────────


def test_a_complete_png_passes(good_png: str) -> None:
    result = pngcheck.inspect(good_png)
    assert result.ok, result.describe()
    assert (result.width, result.height) == (3, 2)
    assert result.colour_type == 2
    assert not result.interlaced
    assert "3x2" in result.describe()


@pytest.mark.parametrize(
    ("label", "expected"),
    [
        ("missing", "not written"),
        ("empty", "zero bytes"),
        ("signature-only", "no IHDR"),
        ("bad-signature", "no PNG signature"),
        ("truncated", "ends at"),
        ("bit-flipped", "CRC mismatch"),
        ("no-iend", "no IEND"),
        ("no-idat", "no image data"),
        ("short-idat", "incomplete render"),
    ],
)
def test_every_way_a_png_can_be_unusable_is_rejected(
    label: str, expected: str, good_png: str, tmp_path
) -> None:
    """A truncated export is the failure mode that matters.

    PowerPoint writing half a slide and stopping is exactly what this is for,
    and it is the case Pillow's verify() accepted -- it checks chunk CRCs but
    never decompresses the pixel data, so `short-idat` below passed it.
    """
    blob = open(good_png, "rb").read()
    path = str(tmp_path / ("%s.png" % label))

    if label == "missing":
        result = pngcheck.inspect(str(tmp_path / "absent.png"))
        assert not result.ok and expected in result.describe()
        return

    mutations = {
        "empty": b"",
        "signature-only": pngcheck.SIGNATURE,
        "bad-signature": b"\x00" * 8 + blob[8:],
        "truncated": blob[: len(blob) - 20],
        "bit-flipped": blob[:-30] + bytes([blob[-30] ^ 0xFF]) + blob[-29:],
        "no-iend": blob[: blob.rfind(b"IEND") - 4],
        "no-idat": pngcheck.SIGNATURE + blob[8 : blob.find(b"IDAT") - 4] + blob[blob.rfind(b"IEND") - 4 :],
        "short-idat": _png_with_short_idat(),
    }
    with open(path, "wb") as fh:
        fh.write(mutations[label])

    result = pngcheck.inspect(path)
    assert not result.ok, "%s was accepted" % label
    assert expected in result.describe(), "%s -> %s" % (label, result.describe())


def _png_with_short_idat() -> bytes:
    """A PNG whose IDAT decompresses cleanly to too few bytes.

    Every CRC is correct and the zlib stream is valid, so this is precisely
    the file a CRC-only check calls healthy.
    """

    def chunk(kind: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + kind
            + data
            + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
        )

    ihdr = struct.pack(">IIBBBBB", 3, 2, 8, 2, 0, 0, 0)
    one_row = b"\x00" + b"\x10" * 9  # one row where IHDR promises two
    return (
        pngcheck.SIGNATURE
        + chunk(b"IHDR", ihdr)
        + chunk(b"IDAT", zlib.compress(one_row))
        + chunk(b"IEND", b"")
    )


def test_undecompressable_idat_is_rejected(tmp_path) -> None:
    def chunk(kind: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + kind
            + data
            + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
        )

    blob = (
        pngcheck.SIGNATURE
        + chunk(b"IHDR", struct.pack(">IIBBBBB", 3, 2, 8, 2, 0, 0, 0))
        + chunk(b"IDAT", b"not a zlib stream")
        + chunk(b"IEND", b"")
    )
    path = str(tmp_path / "bad-stream.png")
    with open(path, "wb") as fh:
        fh.write(blob)
    result = pngcheck.inspect(path)
    assert not result.ok
    assert "will not decompress" in result.describe()


def test_a_zero_dimension_png_is_rejected(tmp_path) -> None:
    def chunk(kind: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + kind
            + data
            + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
        )

    blob = (
        pngcheck.SIGNATURE
        + chunk(b"IHDR", struct.pack(">IIBBBBB", 0, 2, 8, 2, 0, 0, 0))
        + chunk(b"IDAT", zlib.compress(b"\x00"))
        + chunk(b"IEND", b"")
    )
    path = str(tmp_path / "zero.png")
    with open(path, "wb") as fh:
        fh.write(blob)
    assert "degenerate size" in pngcheck.inspect(path).describe()


# ── artifacts.py ────────────────────────────────────────────────────────────


def test_a_run_derives_fixed_names_under_its_root(tmp_path) -> None:
    run = artifacts.Run(str(tmp_path / "run"))
    assert os.path.basename(run.result) == artifacts.RESULT
    assert os.path.basename(run.progress) == artifacts.PROGRESS
    assert os.path.basename(run.pids) == artifacts.PIDS
    assert os.path.relpath(run.slide(7), run.root) == os.path.join(artifacts.SLIDES, "slide-007.png")
    for path in (run.result, run.progress, run.pids, run.slides, run.slide(1)):
        assert path.startswith(run.root + os.sep)


@pytest.mark.parametrize("escape", ["../outside", "a/../../outside", "./../outside"])
def test_contain_rejects_traversal(escape: str, tmp_path) -> None:
    with pytest.raises(ValueError, match="escapes"):
        artifacts.contain(str(tmp_path), escape)


def test_contain_rejects_an_absolute_component(tmp_path) -> None:
    with pytest.raises(ValueError, match="absolute"):
        artifacts.contain(str(tmp_path), os.path.join(os.sep, "etc", "passwd"))


def test_contain_rejects_a_sibling_sharing_the_root_prefix(tmp_path) -> None:
    """The trap a `startswith` check falls into.

    Root `<tmp>/run` and sibling `<tmp>/runx` share a string prefix but the
    second is not inside the first.
    """
    root = tmp_path / "run"
    root.mkdir()
    sibling = tmp_path / "runx"
    sibling.mkdir()
    relative = os.path.relpath(str(sibling / "f"), str(root))
    with pytest.raises(ValueError, match="escapes"):
        artifacts.contain(str(root), relative)


def test_contain_needs_a_component(tmp_path) -> None:
    with pytest.raises(ValueError, match="at least one"):
        artifacts.contain(str(tmp_path))


def test_slide_index_is_one_based(tmp_path) -> None:
    run = artifacts.Run(str(tmp_path))
    with pytest.raises(ValueError, match="1-based"):
        run.slide(0)


def test_clear_removes_only_the_markers_a_run_writes(tmp_path) -> None:
    run = artifacts.Run(str(tmp_path))
    for path in (run.result, run.progress, run.pids):
        with open(path, "w", encoding="utf-8") as fh:
            fh.write("stale")
    keep = run.path("under-test.pptx")
    with open(keep, "w", encoding="utf-8") as fh:
        fh.write("the deck the caller staged here")

    run.clear()

    assert not os.path.exists(run.result)
    assert not os.path.exists(run.progress)
    assert not os.path.exists(run.pids)
    assert os.path.isfile(keep), "clear() must not remove what the caller staged"


def test_clear_is_safe_when_nothing_is_there(tmp_path) -> None:
    artifacts.Run(str(tmp_path / "fresh")).clear()
