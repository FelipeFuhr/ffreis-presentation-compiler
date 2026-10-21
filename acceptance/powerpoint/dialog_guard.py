"""Inspect and clean up a PowerPoint process from the outside. Guest-side.

Everything here is deliberately *about another process*. When PowerPoint puts
up a modal dialog -- "PowerPoint found a problem with content... Repair?" --
the thread that called into COM is parked inside that call, so the process
that opened the file cannot be the one that notices. The parent polls these
functions instead.

Two rules this module exists to enforce:

- **Never click the dialog.** Clicking Repair is how a corrupt package turns
  into a passing test. The dialog's text is read and the process is killed;
  no button is ever pressed.
- **Never touch a PowerPoint we did not start.** Every function here takes an
  explicit PID set. Cleaning up by image name would close the presentation
  someone had open on the same desktop, and the VM is not always the only
  place this runs.
"""

from __future__ import annotations

import queue
import threading
from typing import Iterable

POWERPOINT_IMAGE = "powerpnt.exe"

# The window class Win32 gives every standard dialog box, including the repair
# prompt and every error box PowerPoint raises. A top-level #32770 owned by our
# PowerPoint is, for this harness's purposes, the definition of "it stopped to
# ask a question" -- and a question is a failure, whatever it says.
DIALOG_CLASS = "#32770"

# A modal dialog implies a running message pump (the dialog's own), so reading
# its text cross-process returns promptly. "Implies" is not "guarantees", and a
# wedged PowerPoint would otherwise hang the watcher that exists to catch it,
# so every scan runs on a throwaway thread with a deadline.
SCAN_TIMEOUT_S = 5.0


def powerpoint_pids() -> set[int]:
    """PIDs of every running PowerPoint, by image name.

    Used to diff before/after a DispatchEx so the caller can prove which
    instance is its own.
    """
    import psutil

    pids = set()
    for proc in psutil.process_iter(["pid", "name"]):
        name = (proc.info.get("name") or "").lower()
        if name == POWERPOINT_IMAGE:
            pids.add(int(proc.info["pid"]))
    return pids


def _control_texts(hwnd: int) -> list[str]:
    import win32gui

    texts: list[str] = []

    def collect(child: int, _: object) -> bool:
        try:
            text = win32gui.GetWindowText(child)
        except Exception:  # noqa: BLE001 - a control that died mid-walk
            return True
        if text and text.strip():
            texts.append(text.strip())
        return True

    try:
        win32gui.EnumChildWindows(hwnd, collect, None)
    except Exception:  # noqa: BLE001 - the dialog closed under us
        pass
    return texts


def _scan(pids: set[int]) -> list[dict]:
    import win32gui
    import win32process

    found: list[dict] = []

    def visit(hwnd: int, _: object) -> bool:
        try:
            if not win32gui.IsWindowVisible(hwnd):
                return True
            _, pid = win32process.GetWindowThreadProcessId(hwnd)
            if int(pid) not in pids:
                return True
            if win32gui.GetClassName(hwnd) != DIALOG_CLASS:
                return True
            found.append(
                {
                    "hwnd": int(hwnd),
                    "pid": int(pid),
                    "title": win32gui.GetWindowText(hwnd),
                    "controls": _control_texts(hwnd),
                }
            )
        except Exception:  # noqa: BLE001 - windows come and go mid-enumeration
            return True
        return True

    win32gui.EnumWindows(visit, None)
    return found


def visible_dialogs(pids: Iterable[int]) -> list[dict]:
    """Every visible dialog box owned by one of `pids`.

    Returns a single synthetic entry whose title names the failure if the scan
    itself does not finish, because "the watcher hung" must not read as "no
    dialog".
    """
    wanted = {int(p) for p in pids}
    if not wanted:
        return []

    result: queue.Queue = queue.Queue(maxsize=1)

    def worker() -> None:
        try:
            result.put(_scan(wanted))
        except BaseException as exc:  # noqa: BLE001
            result.put([{"hwnd": 0, "pid": 0, "title": "<scan failed: %s>" % exc, "controls": []}])

    thread = threading.Thread(target=worker, daemon=True)
    thread.start()
    try:
        return result.get(timeout=SCAN_TIMEOUT_S)
    except queue.Empty:
        return [
            {
                "hwnd": 0,
                "pid": 0,
                "title": "<dialog scan did not return within %.0fs>" % SCAN_TIMEOUT_S,
                "controls": [],
            }
        ]


def screenshot(path: str) -> str | None:
    """Grab the whole desktop. Returns the path, or None if it is not possible.

    Needs an interactive session with a real desktop; in a headless or
    session-0 context this fails, and that is reported rather than raised --
    a missing screenshot should never be the reason a test errors.
    """
    try:
        from PIL import ImageGrab

        image = ImageGrab.grab()
        image.save(path)
        return path
    except Exception:  # noqa: BLE001
        return None


def terminate(pids: Iterable[int], grace_s: float = 5.0) -> list[int]:
    """Terminate exactly `pids`, politely then not. Returns those killed.

    Only ever called with PIDs that appeared between a before/after diff
    around our own DispatchEx.
    """
    import psutil

    procs = []
    for pid in {int(p) for p in pids}:
        try:
            procs.append(psutil.Process(pid))
        except psutil.NoSuchProcess:
            continue

    for proc in procs:
        try:
            proc.terminate()
        except psutil.NoSuchProcess:
            pass

    _, alive = psutil.wait_procs(procs, timeout=grace_s)
    for proc in alive:
        try:
            proc.kill()
        except psutil.NoSuchProcess:
            pass
    psutil.wait_procs(alive, timeout=grace_s)
    return [p.pid for p in procs]
