"""Options and fixtures for the PowerPoint acceptance tests.

The prerequisite gate itself lives in `prereq.py`; see the note there for why
it is not an autouse fixture.
"""

from __future__ import annotations

import os
import shutil

import pytest

import prereq


def pytest_addoption(parser: pytest.Parser) -> None:
    group = parser.getgroup("powerpoint")
    group.addoption("--pptx", default=None, help="the .pptx to open (built by the compiler on the host)")
    group.addoption("--expect-slides", type=int, default=None, help="slide count the deck should have")
    group.addoption("--artifacts", default=None, help="directory for diagnostics (screenshots, exports, JSON)")


def pytest_configure(config: pytest.Config) -> None:
    config.addinivalue_line("markers", "powerpoint: needs desktop Microsoft PowerPoint on Windows")
    config.addinivalue_line("markers", "diagnostic: reports a property of this PowerPoint; never asserts it")


@pytest.fixture(scope="session")
def powerpoint_required() -> bool:
    return prereq.is_required()


@pytest.fixture
def powerpoint_gate(powerpoint_required: bool) -> None:
    """Requested by every powerpoint-marked test that does not use `verdict`."""
    prereq.require(powerpoint_required)


@pytest.fixture(scope="session")
def pptx_under_test(request: pytest.FixtureRequest, powerpoint_required: bool) -> str:
    # Gated here, not only in `powerpoint_gate`: this is session-scoped, so
    # pytest builds it before any function-scoped fixture, and without this
    # a machine with no PowerPoint would see "no .pptx given" errors where it
    # should see skips.
    prereq.require(powerpoint_required)

    path = request.config.getoption("--pptx") or os.environ.get(prereq.PPTX_ENV)
    if not path:
        pytest.fail(
            "no .pptx given. Pass --pptx <file> or set %s. The file must be built by this"
            " compiler on the host; the guest has no Go or Chrome." % prereq.PPTX_ENV,
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
    env = os.environ.get(prereq.SLIDES_ENV, "").strip()
    return int(env) if env else None


@pytest.fixture(scope="session")
def artifacts_root(request: pytest.FixtureRequest, tmp_path_factory: pytest.TempPathFactory) -> str:
    chosen = request.config.getoption("--artifacts") or os.environ.get(prereq.ARTIFACTS_ENV)
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
