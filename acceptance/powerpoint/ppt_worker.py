"""Open one .pptx in desktop PowerPoint and export its slides. Guest-side.

This runs as its **own process**, launched by `runner.py`, for one reason: if
PowerPoint decides the file needs repairing it raises a modal dialog, and the
thread that called `Presentations.Open2007` stays inside that call until a
button is clicked. A blocked process cannot report why it is blocked, so the
watching has to happen from outside -- see `dialog_guard.py`.

Two deliberate choices about alerts:

- `DisplayAlerts` is left at PowerPoint's default rather than set to
  `ppAlertsNone`. Suppressing alerts converts a repair prompt into a COM
  error, which is easier to handle but throws away the dialog's text -- and
  that text is the only thing that says *why* PowerPoint objected. The parent
  reads the dialog instead.
- `OpenAndRepair` is passed `msoFalse`. With repair enabled PowerPoint will
  silently fix a malformed package and hand back a working presentation, so
  the test would pass on exactly the files this harness exists to catch.

It takes exactly two paths: the .pptx to open, and one artifacts directory.
Everything it writes is a constant name under that directory, contained by
`artifacts.Run` -- see that module for why the earlier flag-per-file shape was
the wrong one.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import traceback

import artifacts

# MsoTriState. Named here because the positional Open2007 call below is
# unreadable otherwise, and late binding gives no help with argument names.
MSO_TRUE = -1
MSO_FALSE = 0

EXPORT_FILTER = "PNG"
EXPORT_SCALE = 2  # 2x the slide's point size, matching the PNG adapter


def _write_json(path: str, payload: dict) -> None:
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, indent=2, sort_keys=True)


class _Progress:
    """Append-only stage log, flushed every write.

    The parent reads this after a timeout kill to say which call hung.
    """

    def __init__(self, path: str) -> None:
        self._fh = open(path, "w", encoding="utf-8", buffering=1)

    def __call__(self, stage: str) -> None:
        self._fh.write(stage + "\n")

    def close(self) -> None:
        self._fh.close()


def _open_presentation(app: object, pptx: str) -> object:
    return app.Presentations.Open2007(
        pptx,  # FileName
        MSO_TRUE,  # ReadOnly
        MSO_FALSE,  # Untitled
        MSO_TRUE,  # WithWindow
        MSO_FALSE,  # OpenAndRepair -- the whole point
    )


def _export_slides(pres: object, run: artifacts.Run, count: int) -> tuple[list[str], list[int]]:
    width = int(float(pres.PageSetup.SlideWidth) * EXPORT_SCALE)
    height = int(float(pres.PageSetup.SlideHeight) * EXPORT_SCALE)
    os.makedirs(run.slides, exist_ok=True)
    written = []
    for index in range(1, count + 1):
        out = run.slide(index)
        pres.Slides(index).Export(out, EXPORT_FILTER, width, height)
        written.append(out)
    return written, [width, height]


def _version(app: object) -> tuple[str | None, str | None]:
    try:
        version = str(app.Version)
    except AttributeError:
        version = None
    try:
        build = str(app.Build)
    except AttributeError:
        # Build is absent on some versions; Version alone still identifies it.
        build = None
    return version, build


def _run(pptx: str, run: artifacts.Run, result: dict, progress: _Progress) -> None:
    """The COM sequence. Mutates `result` as it goes; raises on any failure."""
    import pythoncom
    import win32com.client

    import dialog_guard

    pythoncom.CoInitialize()

    # Record which POWERPNT processes exist *before* we ask for one, so the
    # ones that appear are provably ours. Cleaning up by image name would
    # kill a PowerPoint the person at the keyboard had open.
    before = dialog_guard.powerpoint_pids()
    result["stage"] = "dispatch"
    progress("dispatch")

    app = win32com.client.DispatchEx("PowerPoint.Application")
    pres = None
    try:
        ours = sorted(dialog_guard.powerpoint_pids() - before)
        # Written before the open, not after: once Open2007 blocks on a dialog
        # this process never reaches another line, and the parent needs to know
        # what to terminate.
        _write_json(run.pids, {"pids": ours, "pre_existing": sorted(before)})
        result["owned_pids"] = ours

        app.Visible = MSO_TRUE
        result["powerpoint_version"], result["powerpoint_build"] = _version(app)

        result["stage"] = "open"
        progress("open")
        pres = _open_presentation(app, pptx)

        result["stage"] = "count"
        progress("count")
        count = int(pres.Slides.Count)
        result["slide_count"] = count

        result["stage"] = "export"
        progress("export")
        result["exports"], result["export_size"] = _export_slides(pres, run, count)

        result["stage"] = "close"
        progress("close")
        pres.Close()
        pres = None
        app.Quit()
        app = None
    finally:
        # Best effort: the parent terminates anything still alive by PID, so a
        # failure to close politely is not itself interesting.
        for close in (
            lambda: pres.Close() if pres is not None else None,
            lambda: app.Quit() if app is not None else None,
        ):
            try:
                close()
            except Exception:  # noqa: BLE001 - teardown of an already-failed run
                pass

    result["ok"] = True
    result["stage"] = "done"
    progress("done")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--pptx", required=True, help="the .pptx to open")
    ap.add_argument(
        "--artifacts-dir",
        required=True,
        help="the one directory this run writes into; every name below it is fixed",
    )
    args = ap.parse_args()

    pptx = os.path.realpath(args.pptx)
    if not os.path.isfile(pptx):
        print("no such .pptx: %s" % pptx, file=sys.stderr)
        return 2

    run = artifacts.Run(args.artifacts_dir)
    progress = _Progress(run.progress)
    result: dict = {
        "ok": False,
        "stage": "start",
        "pptx": pptx,
        "slide_count": None,
        "exports": [],
        "powerpoint_version": None,
        "powerpoint_build": None,
        "error": None,
        "error_type": None,
        "traceback": None,
    }

    try:
        _run(pptx, run, result, progress)
        return 0
    except Exception as exc:
        # Recorded, then re-raised rather than swallowed: the result file is
        # what the parent reads for a verdict, and the traceback on stderr is
        # what a human reads. Returning a tidy exit code here would hide the
        # second one.
        result["error"] = str(exc)
        result["error_type"] = type(exc).__name__
        result["traceback"] = traceback.format_exc()
        progress("error:" + str(result["stage"]))
        raise
    finally:
        _write_json(run.result, result)
        progress.close()


if __name__ == "__main__":
    sys.exit(main())
