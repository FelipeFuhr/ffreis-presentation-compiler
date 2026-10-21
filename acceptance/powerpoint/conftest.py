"""Prerequisites and fixtures for the PowerPoint acceptance tests.

The skip/fail split is the important part. Locally these tests should get out
of the way: almost nobody has a licensed desktop PowerPoint on the machine
they build on, and a suite that fails for everyone is a suite everyone learns
to ignore. But a *job whose entire purpose* is to verify PowerPoint must not
report success because PowerPoint was missing -- a skip that reads as a pass is
how a gate stops gating.

So: absent prerequisites skip, unless POWERPOINT_ACCEPTANCE_REQUIRED is set,
in which case they fail. The designated CI job and the VM runner both set it.
"""

from __future__ import annotations

import os
import shutil
import sys

import pytest

REQUIRED_ENV = "POWERPOINT_ACCEPTANCE_REQUIRED"
PPTX_ENV = "POWERPOINT_ACCEPTANCE_PPTX"
SLIDES_ENV = "POWERPOINT_ACCEPTANCE_SLIDES"
ARTIFACTS_ENV = "POWERPOINT_ACCEPTANCE_ARTIFACTS"


def pytest_addoption(parser: pytest.Parser) -> None:
    group = parser.getgroup("powerpoint")
    group.addoption("--pptx", default=None, help="the .pptx to open (built by the compiler on the host)")
    group.addoption("--expect-slides", type=int, default=None, help="slide count the deck should have")
    group.addoption("--artifacts", default=None, help="directory for diagnostics (screenshots, exports, JSON)")


def pytest_configure(config: pytest.Config) -> None:
    config.addinivalue_line("markers", "powerpoint: needs desktop Microsoft PowerPoint on Windows")
    config.addinivalue_line("markers", "diagnostic: reports a property of this PowerPoint; never asserts it")


def _missing_prerequisites() -> list[str]:
    missing = []
    if sys.platform != "win32":
        missing.append("not running on Windows (sys.platform=%r)" % sys.platform)
        # The rest cannot be probed off-Windows; one reason is enough.
        return missing

    for module in ("win32com.client", "win32gui", "win32ui", "psutil"):
        try:
            __import__(module)
        except ImportError as exc:
            missing.append("python module %s is not importable (%s)" % (module, exc))

    if not _powerpoint_registered():
        missing.append("PowerPoint.Application is not registered (no desktop PowerPoint installed?)")
    return missing


def _powerpoint_registered() -> bool:
    """Is there a COM server for PowerPoint, without starting one?

    Reads the registry rather than calling DispatchEx: starting PowerPoint to
    find out whether PowerPoint exists would make a skip cost 20 seconds and a
    window.
    """
    try:
        import winreg

        with winreg.OpenKey(winreg.HKEY_CLASSES_ROOT, r"PowerPoint.Application\CLSID"):
            return True
    except OSError:
        return False


@pytest.fixture(scope="session")
def powerpoint_required() -> bool:
    return os.environ.get(REQUIRED_ENV, "").strip() not in ("", "0", "false", "no")


@pytest.fixture(scope="session", autouse=True)
def _gate_on_prerequisites(powerpoint_required: bool) -> None:
    missing = _missing_prerequisites()
    if not missing:
        return
    detail = "PowerPoint acceptance prerequisites missing:\n  - " + "\n  - ".join(missing)
    if powerpoint_required:
        pytest.fail(
            detail
            + "\n\n%s is set, so this run was designated to verify PowerPoint and cannot"
            "\nreport success without it." % REQUIRED_ENV,
            pytrace=False,
        )
    pytest.skip(detail + "\n\nSet %s to turn this into a failure." % REQUIRED_ENV, allow_module_level=True)


@pytest.fixture(scope="session")
def pptx_under_test(request: pytest.FixtureRequest) -> str:
    path = request.config.getoption("--pptx") or os.environ.get(PPTX_ENV)
    if not path:
        pytest.fail(
            "no .pptx given. Pass --pptx <file> or set %s. The file must be built by this"
            " compiler on the host; the guest has no Go or Chrome." % PPTX_ENV,
            pytrace=False,
        )
    path = os.path.abspath(path)
    if not os.path.isfile(path):
        pytest.fail("no such .pptx: %s" % path, pytrace=False)
    return path


@pytest.fixture(scope="session")
def expected_slide_count(request: pytest.FixtureRequest) -> int | None:
    value = request.config.getoption("--expect-slides")
    if value is not None:
        return int(value)
    env = os.environ.get(SLIDES_ENV, "").strip()
    return int(env) if env else None


@pytest.fixture(scope="session")
def artifacts_root(request: pytest.FixtureRequest, tmp_path_factory: pytest.TempPathFactory) -> str:
    chosen = request.config.getoption("--artifacts") or os.environ.get(ARTIFACTS_ENV)
    root = os.path.abspath(chosen) if chosen else str(tmp_path_factory.mktemp("powerpoint"))
    os.makedirs(root, exist_ok=True)
    return root


@pytest.fixture
def artifacts_dir(artifacts_root: str, request: pytest.FixtureRequest) -> str:
    path = os.path.join(artifacts_root, request.node.name.replace("/", "_"))
    os.makedirs(path, exist_ok=True)
    return path


@pytest.fixture(scope="session")
def fixture_dir(artifacts_root: str) -> str:
    path = os.path.join(artifacts_root, "fixtures")
    if os.path.isdir(path):
        shutil.rmtree(path)
    os.makedirs(path)
    return path
