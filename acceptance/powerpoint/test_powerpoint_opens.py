"""Does desktop Microsoft PowerPoint actually open what this compiler writes?

Nothing else in this repo answers that question. `make export-selftest` asks
LibreOffice, `pptx_test.go` asserts the bytes, and python-pptx will read almost
anything -- and a file that satisfied all three was still refused by a desktop
PowerPoint, because it declared a ZIP version of 0.0 that only a strict OPC
parser objects to. Every lenient reader in the toolchain agreed the file was
fine. The only way to know is to ask PowerPoint.

Read `README.md` in this directory before changing anything here; the
no-repair, own-instance-only and never-click-the-dialog rules are load-bearing,
not stylistic.
"""

from __future__ import annotations

import json
import os

import pytest

import fixtures
import pngcheck
import prereq
import runner

pytestmark = pytest.mark.powerpoint


@pytest.fixture(scope="session")
def verdict(powerpoint_required: bool, pptx_under_test: str, artifacts_root: str) -> runner.Verdict:
    """One open attempt on the real artifact, shared by every assertion below.

    Session-scoped deliberately: launching PowerPoint costs tens of seconds and
    a desktop, and splitting "it opened" from "the slides exported" across two
    launches would double that to assert two properties of the same open.
    """
    prereq.require(powerpoint_required)
    out = runner.open_in_powerpoint(pptx_under_test, os.path.join(artifacts_root, "compiler-output"))
    with open(os.path.join(artifacts_root, "verdict.json"), "w", encoding="utf-8") as fh:
        json.dump(
            {
                "outcome": out.outcome,
                "reason": out.reason,
                "slide_count": out.slide_count,
                "powerpoint_version": out.powerpoint_version,
                "dialogs": out.dialogs,
                "exports": len(out.exports),
            },
            fh,
            indent=2,
        )
    return out


def test_powerpoint_raised_no_dialog(verdict: runner.Verdict) -> None:
    """No repair prompt, no error box, no modal anything.

    Asserted before the open result itself, because a dialog is the failure
    this whole harness exists for and its text is the only diagnostic that
    names a reason.
    """
    assert verdict.dialogs == [], "PowerPoint stopped to ask something:\n" + verdict.describe()


def test_powerpoint_opened_the_file_without_repair(verdict: runner.Verdict) -> None:
    assert verdict.outcome != runner.PRECONDITION, verdict.reason
    assert verdict.outcome != runner.TIMEOUT, (
        "inconclusive -- no verdict before the deadline. This is not a pass:\n" + verdict.describe()
    )
    assert verdict.accepted, "PowerPoint did not open the compiler's output:\n" + verdict.describe()


def test_slide_count_matches_the_deck(verdict: runner.Verdict, expected_slide_count: int | None) -> None:
    if expected_slide_count is None:
        pytest.skip("no expected slide count given (--expect-slides / POWERPOINT_ACCEPTANCE_SLIDES)")
    assert verdict.slide_count == expected_slide_count, (
        "PowerPoint sees %s slides, the deck declares %s" % (verdict.slide_count, expected_slide_count)
    )


def test_every_slide_exports_to_a_readable_image(verdict: runner.Verdict) -> None:
    """A presentation can open and still not render.

    PowerPoint will happily open a package whose slide XML it cannot draw, so
    "opened" is necessary and not sufficient. Exporting every slide forces it
    to actually rasterise each one, and `pngcheck` then decompresses the pixel
    data and compares its length against the size IHDR declares -- so a slide
    PowerPoint began writing and abandoned is a finding, not a pass.
    """
    assert verdict.exports, "no slides were exported:\n" + verdict.describe()
    assert len(verdict.exports) == verdict.slide_count, (
        "exported %d images for %s slides" % (len(verdict.exports), verdict.slide_count)
    )

    problems = []
    for path in verdict.exports:
        result = pngcheck.inspect(path)
        if not result.ok:
            problems.append("%s: %s" % (os.path.basename(path), result.describe()))

    assert not problems, "slide exports are not readable:\n  " + "\n  ".join(problems)


# ── Negative control ────────────────────────────────────────────────────────
#
# Without this, a harness that silently stopped detecting rejection would look
# exactly like a run of clean passes -- the same reason this repo ships
# check-selftest and validate-selftest.


def test_the_harness_detects_a_package_powerpoint_must_reject(
    powerpoint_gate: None, pptx_under_test: str, fixture_dir: str, artifacts_root: str
) -> None:
    broken = fixtures.without_content_types(
        pptx_under_test, os.path.join(fixture_dir, "no-content-types.pptx")
    )
    out = runner.open_in_powerpoint(broken, os.path.join(artifacts_root, "negative-control"))

    assert out.outcome != runner.PRECONDITION, out.reason
    assert out.outcome != runner.TIMEOUT, (
        "the negative control neither opened nor was refused before the deadline, so this run"
        " cannot show the harness detects rejection:\n" + out.describe()
    )
    assert out.rejected, (
        "PowerPoint ACCEPTED a package with no [Content_Types].xml. Either repair ran anyway or"
        " this harness is not observing what it thinks it is -- every positive result in this"
        " file is void until that is explained:\n" + out.describe()
    )


# ── Diagnostic ──────────────────────────────────────────────────────────────


@pytest.mark.diagnostic
def test_report_whether_this_powerpoint_rejects_zip_version_zero(
    powerpoint_gate: None, pptx_under_test: str, fixture_dir: str, artifacts_root: str
) -> None:
    """Record whether *this* PowerPoint is strict about the field that shipped.

    This is the defect that started all of this: every entry declaring "version
    needed to extract 0.0". It is advisory in the ZIP spec, so a reader is
    within its rights either way -- which makes a pass/fail assertion here a
    statement about the installed PowerPoint, not about the compiler. So it is
    recorded, not asserted.

    The value is in running it on more than one version: it is what turns "a
    client could not open it" into "PowerPoint <version> rejects this field".
    """
    probe = fixtures.with_zero_zip_versions(
        pptx_under_test, os.path.join(fixture_dir, "zero-zip-versions.pptx")
    )
    declared = fixtures.declared_versions(probe)
    assert declared["local_version_needed"] == [0], "fixture did not take: %r" % declared

    out = runner.open_in_powerpoint(probe, os.path.join(artifacts_root, "zip-version-zero"))
    report = {
        "powerpoint_version": out.powerpoint_version,
        "outcome": out.outcome,
        "reason": out.reason,
        "strict_about_zip_version": bool(out.rejected),
        "dialogs": out.dialogs,
        "declared": declared,
    }
    path = os.path.join(artifacts_root, "zip-version-zero-report.json")
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(report, fh, indent=2)

    print(
        "\nzip-version-0.0 diagnostic: PowerPoint %s %s this file (%s)\n  report: %s"
        % (
            out.powerpoint_version or "?",
            "REJECTS" if out.rejected else "accepts",
            out.outcome,
            path,
        )
    )

    # The one real assertion: the probe has to reach a verdict. A timeout here
    # means the diagnostic measured nothing.
    assert out.outcome != runner.TIMEOUT, "no verdict on the zip-version probe:\n" + out.describe()
