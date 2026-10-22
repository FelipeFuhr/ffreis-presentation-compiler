"""Is this machine able to answer the PowerPoint question at all?

Kept out of `conftest.py` so that both the fixtures and the session-scoped
`verdict` fixture can call it directly. An autouse gate cannot do that job:
pytest sets higher-scoped fixtures up first, so a session-scoped `verdict`
would run *before* a function-scoped autouse gate and fail on a machine the
gate was about to skip.

The skip/fail split is the point. Locally these tests should get out of the
way -- almost nobody has a licensed desktop PowerPoint on the machine they
build on, and a suite that fails for everyone is a suite everyone learns to
ignore. But a *run whose entire purpose* is to verify PowerPoint must not
report success because PowerPoint was missing: a skip that reads as a pass is
how a gate stops gating.
"""

from __future__ import annotations

import os
import sys

import pytest

REQUIRED_ENV = "POWERPOINT_ACCEPTANCE_REQUIRED"
PPTX_ENV = "POWERPOINT_ACCEPTANCE_PPTX"
SLIDES_ENV = "POWERPOINT_ACCEPTANCE_SLIDES"
ARTIFACTS_ENV = "POWERPOINT_ACCEPTANCE_ARTIFACTS"

_GUEST_MODULES = ("win32com.client", "win32gui", "win32ui", "psutil")


def is_required() -> bool:
    return os.environ.get(REQUIRED_ENV, "").strip().lower() not in ("", "0", "false", "no")


def _powerpoint_registered() -> bool:
    """Is there a COM server for PowerPoint, without starting one?

    Reads the registry rather than calling DispatchEx: starting PowerPoint to
    find out whether PowerPoint exists would make a skip cost 20 seconds and
    a window.
    """
    try:
        import winreg

        with winreg.OpenKey(winreg.HKEY_CLASSES_ROOT, r"PowerPoint.Application\CLSID"):
            return True
    except OSError:
        return False


def missing() -> list[str]:
    """Every prerequisite this machine lacks, most fundamental first."""
    if sys.platform != "win32":
        # Nothing below can even be probed off-Windows; one reason is enough.
        return ["not running on Windows (sys.platform=%r)" % sys.platform]

    gaps = []
    for module in _GUEST_MODULES:
        try:
            __import__(module)
        except ImportError as exc:
            gaps.append("python module %s is not importable (%s)" % (module, exc))
    if not _powerpoint_registered():
        gaps.append("PowerPoint.Application is not registered (no desktop PowerPoint installed?)")
    return gaps


def require(required: bool | None = None) -> None:
    """Skip -- or, when this run was designated to verify PowerPoint, fail."""
    gaps = missing()
    if not gaps:
        return
    if required is None:
        required = is_required()
    detail = "PowerPoint acceptance prerequisites missing:\n  - " + "\n  - ".join(gaps)
    if required:
        pytest.fail(
            detail
            + "\n\n%s is set, so this run was designated to verify PowerPoint and"
            "\ncannot report success without it." % REQUIRED_ENV,
            pytrace=False,
        )
    pytest.skip(detail + "\n\nSet %s to turn this into a failure." % REQUIRED_ENV)
