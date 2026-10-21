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

Writes a JSON result to `--result` and a one-line stage marker to
`--progress`, so the parent still learns where it got to if it has to be
killed.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import traceback

# MsoTriState. Named here because the positional Open2007 call below is
# unreadable otherwise, and late binding gives no help with argument names.
MSO_TRUE = -1
MSO_FALSE = 0

EXPORT_FILTER = "PNG"
EXPORT_SCALE = 2  # 2x the slide's point size, matching the PNG adapter


def _write(path: str, payload: dict) -> None:
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


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--pptx", required=True)
    ap.add_argument("--out-dir", required=True)
    ap.add_argument("--result", required=True)
    ap.add_argument("--progress", required=True)
    ap.add_argument("--pid-file", required=True)
    args = ap.parse_args()

    os.makedirs(args.out_dir, exist_ok=True)
    progress = _Progress(args.progress)

    result: dict = {
        "ok": False,
        "stage": "start",
        "pptx": os.path.abspath(args.pptx),
        "slide_count": None,
        "exports": [],
        "powerpoint_version": None,
        "powerpoint_build": None,
        "error": None,
        "error_type": None,
        "traceback": None,
    }

    app = None
    pres = None
    try:
        import pythoncom  # noqa: PLC0415  (guest-only dependency)
        import win32com.client  # noqa: PLC0415

        import dialog_guard

        pythoncom.CoInitialize()

        # Record which POWERPNT processes exist *before* we ask for one, so the
        # ones that appear are provably ours. Cleaning up by image name would
        # kill a PowerPoint the person at the keyboard had open.
        before = dialog_guard.powerpoint_pids()
        result["stage"] = "dispatch"
        progress("dispatch")

        app = win32com.client.DispatchEx("PowerPoint.Application")

        ours = sorted(dialog_guard.powerpoint_pids() - before)
        # Written before the open, not after: once Open2007 blocks on a dialog
        # this process never reaches another line, and the parent needs to know
        # what to terminate.
        _write(args.pid_file, {"pids": ours, "pre_existing": sorted(before)})
        result["owned_pids"] = ours

        app.Visible = MSO_TRUE
        result["powerpoint_version"] = str(app.Version)
        try:
            result["powerpoint_build"] = str(app.Build)
        except Exception:  # noqa: BLE001 - Build is absent on some versions
            result["powerpoint_build"] = None

        result["stage"] = "open"
        progress("open")
        pres = app.Presentations.Open2007(
            os.path.abspath(args.pptx),  # FileName
            MSO_TRUE,  # ReadOnly
            MSO_FALSE,  # Untitled
            MSO_TRUE,  # WithWindow
            MSO_FALSE,  # OpenAndRepair -- the whole point
        )

        result["stage"] = "count"
        progress("count")
        count = int(pres.Slides.Count)
        result["slide_count"] = count

        width = int(float(pres.PageSetup.SlideWidth) * EXPORT_SCALE)
        height = int(float(pres.PageSetup.SlideHeight) * EXPORT_SCALE)
        result["export_size"] = [width, height]

        result["stage"] = "export"
        progress("export")
        for index in range(1, count + 1):
            out = os.path.join(args.out_dir, "slide-%03d.png" % index)
            pres.Slides(index).Export(out, EXPORT_FILTER, width, height)
            result["exports"].append(out)

        result["stage"] = "close"
        progress("close")
        pres.Close()
        pres = None
        app.Quit()
        app = None

        result["ok"] = True
        result["stage"] = "done"
        progress("done")
        return 0

    except BaseException as exc:  # noqa: BLE001 - every failure is a result
        result["error"] = str(exc)
        result["error_type"] = type(exc).__name__
        result["traceback"] = traceback.format_exc()
        progress("error:" + result["stage"])
        # Best-effort teardown. A failure here is not interesting -- the parent
        # terminates anything still alive by PID regardless.
        for close in (
            lambda: pres.Close() if pres is not None else None,
            lambda: app.Quit() if app is not None else None,
        ):
            try:
                close()
            except Exception:  # noqa: BLE001,S110
                pass
        return 1

    finally:
        _write(args.result, result)
        progress.close()


if __name__ == "__main__":
    sys.exit(main())
