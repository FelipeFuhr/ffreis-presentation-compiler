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

The window walks below use `GetWindow(GW_CHILD/GW_HWNDNEXT)` rather than
`EnumWindows`/`EnumChildWindows`. pywin32 stops enumerating when a callback
returns anything falsy, so a callback here would have to `return True`
unconditionally -- a function whose value never varies, for no reason beyond
the callback contract. Walking the sibling chain says the same thing with no
callback at all.
"""

from __future__ import annotations

import queue
import threading
from typing import Iterable, Iterator

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

_GW_HWNDNEXT = 2
_GW_CHILD = 5


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


def _siblings(first: int) -> Iterator[int]:
    """Walk a window's sibling chain from `first`, skipping dead handles."""
    import win32gui

    hwnd = first
    while hwnd:
        yield hwnd
        try:
            hwnd = win32gui.GetWindow(hwnd, _GW_HWNDNEXT)
        except Exception:  # noqa: BLE001 - pywintypes.error on a vanished window
            return


def _top_level() -> Iterator[int]:
    import win32gui

    try:
        first = win32gui.GetWindow(win32gui.GetDesktopWindow(), _GW_CHILD)
    except Exception:  # noqa: BLE001 - no desktop (session 0)
        return
    yield from _siblings(first)


def _control_texts(hwnd: int) -> list[str]:
    """Every non-empty caption among `hwnd`'s child controls.

    This is where the dialog's actual message lives: the window title is
    usually just "Microsoft PowerPoint".
    """
    import win32gui

    texts: list[str] = []
    try:
        first = win32gui.GetWindow(hwnd, _GW_CHILD)
    except Exception:  # noqa: BLE001 - the dialog closed under us
        return texts

    for child in _siblings(first):
        try:
            text = win32gui.GetWindowText(child)
        except Exception:  # noqa: BLE001 - a control that died mid-walk
            continue
        if text and text.strip():
            texts.append(text.strip())
    return texts


def _scan(pids: set[int]) -> list[dict]:
    import win32gui
    import win32process

    found: list[dict] = []
    for hwnd in _top_level():
        try:
            if not win32gui.IsWindowVisible(hwnd):
                continue
            _, pid = win32process.GetWindowThreadProcessId(hwnd)
            if int(pid) not in pids:
                continue
            if win32gui.GetClassName(hwnd) != DIALOG_CLASS:
                continue
            title = win32gui.GetWindowText(hwnd)
        except Exception:  # noqa: BLE001 - windows come and go mid-walk
            continue
        found.append(
            {
                "hwnd": int(hwnd),
                "pid": int(pid),
                "title": title,
                "controls": _control_texts(hwnd),
            }
        )
    return found


def _synthetic(title: str) -> list[dict]:
    """A finding that reports the watcher's own failure, not PowerPoint's.

    "The scan hung" must never reach a test as "no dialog".
    """
    return [{"hwnd": 0, "pid": 0, "title": title, "controls": []}]


def visible_dialogs(pids: Iterable[int]) -> list[dict]:
    """Every visible dialog box owned by one of `pids`."""
    wanted = {int(p) for p in pids}
    if not wanted:
        return []

    result: queue.Queue = queue.Queue(maxsize=1)

    def worker() -> None:
        try:
            result.put(_scan(wanted))
        except BaseException as exc:  # noqa: BLE001 - reported, never swallowed
            result.put(_synthetic("<scan failed: %s>" % exc))

    threading.Thread(target=worker, daemon=True).start()
    try:
        return result.get(timeout=SCAN_TIMEOUT_S)
    except queue.Empty:
        return _synthetic("<dialog scan did not return within %.0fs>" % SCAN_TIMEOUT_S)


def screenshot(path: str) -> str | None:
    """Grab the whole desktop to a .bmp. Returns the path, or None.

    Done with GDI through pywin32 rather than Pillow: Pillow was carried for
    this one call plus a readability check, and brought a dozen critical
    advisories in decoders this harness never touches. BMP is what
    SaveBitmapFile writes and costs nothing to read.

    Needs an interactive session with a real desktop. In a headless or
    session-0 context this fails, and that is reported rather than raised --
    a missing screenshot must never be the reason a test errors.
    """
    desktop = device = src = mem = bitmap = None
    try:
        import win32con
        import win32gui
        import win32ui

        desktop = win32gui.GetDesktopWindow()
        left, top, right, bottom = win32gui.GetWindowRect(desktop)
        width, height = right - left, bottom - top

        device = win32gui.GetWindowDC(desktop)
        src = win32ui.CreateDCFromHandle(device)
        mem = src.CreateCompatibleDC()
        bitmap = win32ui.CreateBitmap()
        bitmap.CreateCompatibleBitmap(src, width, height)
        mem.SelectObject(bitmap)
        mem.BitBlt((0, 0), (width, height), src, (left, top), win32con.SRCCOPY)
        bitmap.SaveBitmapFile(mem, path)
        return path
    except Exception:  # noqa: BLE001 - no desktop, no GDI, no screenshot
        return None
    finally:
        _release_gdi(desktop, device, src, mem, bitmap)


def _release_gdi(desktop: object, device: object, src: object, mem: object, bitmap: object) -> None:
    """Hand back the GDI handles screenshot() took.

    These are a finite, process-wide resource and this runs on every failure
    path, so leaking them would degrade the next run rather than this one.
    """
    import win32gui

    releases = (
        lambda: mem.DeleteDC(),
        lambda: src.DeleteDC(),
        lambda: win32gui.ReleaseDC(desktop, device),
        lambda: win32gui.DeleteObject(bitmap.GetHandle()),
    )
    for release in releases:
        try:
            release()
        except Exception:  # noqa: BLE001 - never created, or already gone
            continue


def terminate(pids: Iterable[int], grace_s: float = 5.0) -> list[int]:
    """Terminate exactly `pids`, politely then not. Returns those addressed.

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
            continue

    _, alive = psutil.wait_procs(procs, timeout=grace_s)
    for proc in alive:
        try:
            proc.kill()
        except psutil.NoSuchProcess:
            continue
    psutil.wait_procs(alive, timeout=grace_s)
    return [p.pid for p in procs]
