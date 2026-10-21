"""Drive one open-in-PowerPoint attempt and return a verdict. Guest-side.

The worker (`ppt_worker.py`) runs as a child process so that this one stays
responsive while PowerPoint is blocked. This module is the deadline and the
watcher: it polls for a modal dialog, enforces a hard timeout, and guarantees
that whatever PowerPoint the worker started is gone before it returns.

The verdict deliberately distinguishes four outcomes, because they mean
different things to a test:

  opened    PowerPoint accepted the file and the slides exported.
  dialog    PowerPoint stopped to ask something -- a repair prompt or an
            error box. Treated as a rejection, with the text captured.
  error     The COM call failed outright (an exception, no dialog). Also a
            rejection, and the normal one when a package is malformed enough.
  timeout   Nothing came back in time. Not a pass and not a clean rejection;
            an inconclusive run that needs a human.
"""

from __future__ import annotations

import dataclasses
import json
import os
import subprocess
import sys
import time

import dialog_guard

# Generous: a cold PowerPoint on a fresh VM snapshot can take tens of seconds
# to first paint, and exporting every slide to PNG is not instant either. The
# point of the deadline is to end a *hang*, not to measure performance.
DEFAULT_TIMEOUT_S = 240.0
POLL_INTERVAL_S = 1.0

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
            for text in dialog.get("controls", []):
                lines.append("      | %s" % text)
        if self.screenshot:
            lines.append("  screenshot: %s" % self.screenshot)
        if self.worker_stderr.strip():
            lines.append("  worker stderr:")
            lines.extend("      | %s" % ln for ln in self.worker_stderr.strip().splitlines())
        return "\n".join(lines)


def _read_json(path: str) -> dict:
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh)
    except Exception:  # noqa: BLE001 - absent or half-written is normal here
        return {}


def _read_text(path: str) -> str:
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except Exception:  # noqa: BLE001
        return ""


def open_in_powerpoint(
    pptx: str,
    artifacts_dir: str,
    timeout_s: float = DEFAULT_TIMEOUT_S,
) -> Verdict:
    """Open `pptx` once in a PowerPoint instance created for this call."""
    os.makedirs(artifacts_dir, exist_ok=True)
    slides_dir = os.path.join(artifacts_dir, "slides")
    result_path = os.path.join(artifacts_dir, "worker-result.json")
    progress_path = os.path.join(artifacts_dir, "worker-progress.log")
    pid_path = os.path.join(artifacts_dir, "worker-pids.json")
    for stale in (result_path, progress_path, pid_path):
        if os.path.exists(stale):
            os.remove(stale)

    # Refuse rather than share. DispatchEx is documented to start a new server,
    # but PowerPoint is not reliably multi-instance across versions, so an
    # already-running copy could be handed back -- and then this harness would
    # be driving, and later terminating, someone's open work.
    pre_existing = dialog_guard.powerpoint_pids()
    if pre_existing:
        return Verdict(
            outcome=PRECONDITION,
            reason=(
                "PowerPoint is already running (pids %s). This harness will not drive or "
                "terminate an instance it did not create -- close PowerPoint and re-run."
                % sorted(pre_existing)
            ),
        )

    worker = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ppt_worker.py")
    proc = subprocess.Popen(
        [
            sys.executable,
            "-u",
            worker,
            "--pptx", pptx,
            "--out-dir", slides_dir,
            "--result", result_path,
            "--progress", progress_path,
            "--pid-file", pid_path,
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        cwd=os.path.dirname(os.path.abspath(__file__)),
    )

    deadline = time.monotonic() + timeout_s
    dialogs: list[dict] = []
    shot: str | None = None
    timed_out = False

    while True:
        if proc.poll() is not None:
            break

        owned = _read_json(pid_path).get("pids") or []
        if owned:
            dialogs = dialog_guard.visible_dialogs(owned)
            if dialogs:
                shot = dialog_guard.screenshot(os.path.join(artifacts_dir, "dialog.bmp"))
                break

        if time.monotonic() >= deadline:
            timed_out = True
            owned = _read_json(pid_path).get("pids") or []
            dialogs = dialog_guard.visible_dialogs(owned)
            shot = dialog_guard.screenshot(os.path.join(artifacts_dir, "timeout.bmp"))
            break

        time.sleep(POLL_INTERVAL_S)

    owned = _read_json(pid_path).get("pids") or []
    if proc.poll() is None:
        # Kill PowerPoint first: the worker is parked inside a COM call and will
        # not return until the thing it is calling is gone.
        dialog_guard.terminate(owned)
        try:
            proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.kill()
    stdout, stderr = proc.communicate()
    dialog_guard.terminate(owned)

    result = _read_json(result_path)
    stage = (_read_text(progress_path).strip().splitlines() or [None])[-1]
    common = {
        "stage": stage,
        "slide_count": result.get("slide_count"),
        "exports": result.get("exports") or [],
        "dialogs": dialogs,
        "screenshot": shot,
        "powerpoint_version": result.get("powerpoint_version"),
        "worker_exit": proc.returncode,
        "worker_stdout": stdout or "",
        "worker_stderr": stderr or "",
        "result": result,
    }

    if dialogs:
        titles = ", ".join(repr(d.get("title") or "<untitled>") for d in dialogs)
        return Verdict(DIALOG, "PowerPoint raised a dialog (%s) and was not answered" % titles, **common)
    if timed_out:
        return Verdict(TIMEOUT, "no verdict within %.0fs" % timeout_s, **common)
    if result.get("ok"):
        return Verdict(OPENED, "opened and exported %s slides" % result.get("slide_count"), **common)
    return Verdict(
        ERROR,
        "PowerPoint refused the file: %s: %s"
        % (result.get("error_type") or "exit %s" % proc.returncode, result.get("error") or "no detail"),
        **common,
    )
