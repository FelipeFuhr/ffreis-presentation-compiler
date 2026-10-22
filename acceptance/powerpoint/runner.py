"""Drive one open-in-PowerPoint attempt and return a verdict. Guest-side.

The worker (`ppt_worker.py`) runs as a child process so that this one stays
responsive while PowerPoint is blocked. This module is the deadline and the
watcher: it polls for a modal dialog, enforces a hard timeout, and guarantees
that whatever PowerPoint the worker started is gone before it returns.

The verdict deliberately distinguishes five outcomes, because they mean
different things to a test:

  opened        PowerPoint accepted the file and the slides exported.
  dialog        PowerPoint stopped to ask something -- a repair prompt or an
                error box. A rejection, with the text captured.
  error         The COM call failed outright (an exception, no dialog). Also a
                rejection, and the normal one for a malformed package.
  timeout       Nothing came back in time. Not a pass and not a clean
                rejection: an inconclusive run that needs a human.
  precondition  We declined to run at all (a PowerPoint was already open).
"""

from __future__ import annotations

import dataclasses
import json
import os
import subprocess
import sys
import time

import artifacts
import dialog_guard

# Generous: a cold PowerPoint on a fresh VM snapshot can take tens of seconds
# to first paint, and exporting every slide to PNG is not instant either. The
# point of the deadline is to end a *hang*, not to measure performance.
DEFAULT_TIMEOUT_S = 240.0
POLL_INTERVAL_S = 1.0
WORKER_REAP_S = 15.0

OPENED = "opened"
DIALOG = "dialog"
ERROR = "error"
TIMEOUT = "timeout"
PRECONDITION = "precondition"


@dataclasses.dataclass
class Verdict:
    outcome: str
    reason: str
    stage: str | None = None
    slide_count: int | None = None
    exports: list[str] = dataclasses.field(default_factory=list)
    dialogs: list[dict] = dataclasses.field(default_factory=list)
    screenshot: str | None = None
    powerpoint_version: str | None = None
    worker_exit: int | None = None
    worker_stdout: str = ""
    worker_stderr: str = ""
    result: dict = dataclasses.field(default_factory=dict)

    @property
    def accepted(self) -> bool:
        """True only for a clean open. A timeout is never an acceptance."""
        return self.outcome == OPENED

    @property
    def rejected(self) -> bool:
        """True when PowerPoint actively refused the file."""
        return self.outcome in (DIALOG, ERROR)

    def describe(self) -> str:
        lines = ["outcome=%s: %s" % (self.outcome, self.reason)]
        if self.stage:
            lines.append("  worker reached stage: %s" % self.stage)
        if self.powerpoint_version:
            lines.append("  PowerPoint version: %s" % self.powerpoint_version)
        for dialog in self.dialogs:
            lines.append("  dialog %r" % (dialog.get("title") or "<untitled>"))
            lines.extend("      | %s" % text for text in dialog.get("controls", []))
        if self.screenshot:
            lines.append("  screenshot: %s" % self.screenshot)
        if self.worker_stderr.strip():
            lines.append("  worker stderr:")
            lines.extend("      | %s" % ln for ln in self.worker_stderr.strip().splitlines())
        return "\n".join(lines)


@dataclasses.dataclass
class _Watch:
    """What the polling loop observed, before it is turned into a Verdict."""

    dialogs: list[dict] = dataclasses.field(default_factory=list)
    screenshot: str | None = None
    timed_out: bool = False


def _read_json(path: str) -> dict:
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, ValueError):
        # Absent or half-written is the normal case while the worker runs.
        return {}


def _read_text(path: str) -> str:
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except OSError:
        return ""


def _owned_pids(run: artifacts.Run) -> list[int]:
    return _read_json(run.pids).get("pids") or []


def _spawn(pptx: str, run: artifacts.Run) -> subprocess.Popen:
    worker = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ppt_worker.py")
    return subprocess.Popen(
        [sys.executable, "-u", worker, "--pptx", pptx, "--artifacts-dir", run.root],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        cwd=os.path.dirname(os.path.abspath(__file__)),
    )


def _watch(proc: subprocess.Popen, run: artifacts.Run, timeout_s: float) -> _Watch:
    """Poll until the worker exits, a dialog appears, or the deadline passes."""
    deadline = time.monotonic() + timeout_s
    while proc.poll() is None:
        dialogs = dialog_guard.visible_dialogs(_owned_pids(run))
        if dialogs:
            return _Watch(dialogs, dialog_guard.screenshot(run.path(artifacts.DIALOG_SHOT)))
        if time.monotonic() >= deadline:
            return _Watch(
                dialog_guard.visible_dialogs(_owned_pids(run)),
                dialog_guard.screenshot(run.path(artifacts.TIMEOUT_SHOT)),
                timed_out=True,
            )
        time.sleep(POLL_INTERVAL_S)
    return _Watch()


def _reap(proc: subprocess.Popen, run: artifacts.Run) -> tuple[str, str]:
    """Make sure neither the worker nor its PowerPoint survives this call."""
    owned = _owned_pids(run)
    if proc.poll() is None:
        # Kill PowerPoint first: the worker is parked inside a COM call and
        # will not return until the thing it is calling is gone.
        dialog_guard.terminate(owned)
        try:
            proc.wait(timeout=WORKER_REAP_S)
        except subprocess.TimeoutExpired:
            proc.kill()
    stdout, stderr = proc.communicate()
    dialog_guard.terminate(owned)
    return stdout or "", stderr or ""


def _decide(watch: _Watch, result: dict, stage: str | None, returncode: int | None, timeout_s: float,
            stdout: str, stderr: str) -> Verdict:
    common = {
        "stage": stage,
        "slide_count": result.get("slide_count"),
        "exports": result.get("exports") or [],
        "dialogs": watch.dialogs,
        "screenshot": watch.screenshot,
        "powerpoint_version": result.get("powerpoint_version"),
        "worker_exit": returncode,
        "worker_stdout": stdout,
        "worker_stderr": stderr,
        "result": result,
    }
    if watch.dialogs:
        titles = ", ".join(repr(d.get("title") or "<untitled>") for d in watch.dialogs)
        return Verdict(DIALOG, "PowerPoint raised a dialog (%s) and was not answered" % titles, **common)
    if watch.timed_out:
        return Verdict(TIMEOUT, "no verdict within %.0fs" % timeout_s, **common)
    if result.get("ok"):
        return Verdict(OPENED, "opened and exported %s slides" % result.get("slide_count"), **common)
    detail = result.get("error") or "no detail"
    kind = result.get("error_type") or "exit %s" % returncode
    return Verdict(ERROR, "PowerPoint refused the file: %s: %s" % (kind, detail), **common)


def open_in_powerpoint(
    pptx: str,
    artifacts_dir: str,
    timeout_s: float = DEFAULT_TIMEOUT_S,
) -> Verdict:
    """Open `pptx` once, in a PowerPoint instance created for this call."""
    run = artifacts.Run(artifacts_dir)
    run.clear()

    # Refuse rather than share. DispatchEx is documented to start a new server,
    # but PowerPoint is not reliably multi-instance across versions, so an
    # already-running copy could be handed back -- and then this harness would
    # be driving, and later terminating, someone's open work.
    pre_existing = dialog_guard.powerpoint_pids()
    if pre_existing:
        return Verdict(
            PRECONDITION,
            "PowerPoint is already running (pids %s). This harness will not drive or "
            "terminate an instance it did not create -- close PowerPoint and re-run."
            % sorted(pre_existing),
        )

    proc = _spawn(pptx, run)
    watch = _watch(proc, run, timeout_s)
    stdout, stderr = _reap(proc, run)

    result = _read_json(run.result)
    stage = (_read_text(run.progress).strip().splitlines() or [None])[-1]
    return _decide(watch, result, stage, proc.returncode, timeout_s, stdout, stderr)
