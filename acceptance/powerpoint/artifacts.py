"""One contained directory for everything a run writes. Runs anywhere.

Every file this harness produces -- the worker's result, its progress log, its
PID file, the slide exports, screenshots, fixtures -- lives under a single root
passed in as one argument. Earlier each of those was its own CLI flag, which
meant four separate argv-derived paths opened for writing, and
`internal/pathsafe` exists in this repo precisely because that shape is the
security-sensitive one.

So the rule here is the same one the compiler applies to deck- and
theme-authored paths: a caller supplies the root, every path below it is a
constant this module owns, and `contain` is what proves a joined path did not
escape. `filepath.Join`'s Python equivalent does not stop `..`, and a
`startswith` check on the joined string is its own trap -- root `/a/run` has
the same prefix as the unrelated sibling `/a/runx/f`.
"""

from __future__ import annotations

import os

# The fixed names a run writes. Constants rather than arguments: an attacker
# (or a typo) can choose the root, never a name below it.
RESULT = "worker-result.json"
PROGRESS = "worker-progress.log"
PIDS = "worker-pids.json"
SLIDES = "slides"
DIALOG_SHOT = "dialog.bmp"
TIMEOUT_SHOT = "timeout.bmp"


def contain(root: str, *parts: str) -> str:
    """Join `parts` under `root`, refusing anything that escapes it.

    Mirrors internal/pathsafe.Contain: resolve, then ask `relpath` whether the
    result is still below the root, rather than comparing string prefixes.
    """
    if not parts:
        raise ValueError("contain needs at least one path component")
    for part in parts:
        if os.path.isabs(part):
            raise ValueError("refusing an absolute component: %r" % part)
    base = os.path.realpath(root)
    target = os.path.realpath(os.path.join(base, *parts))
    relative = os.path.relpath(target, base)
    if relative == os.pardir or relative.startswith(os.pardir + os.sep):
        raise ValueError("%r escapes %r" % (os.path.join(*parts), root))
    return target


class Run:
    """The one directory a single open-attempt writes into."""

    def __init__(self, root: str) -> None:
        self.root = os.path.realpath(root)
        os.makedirs(self.root, exist_ok=True)

    def path(self, *parts: str) -> str:
        return contain(self.root, *parts)

    @property
    def result(self) -> str:
        return self.path(RESULT)

    @property
    def progress(self) -> str:
        return self.path(PROGRESS)

    @property
    def pids(self) -> str:
        return self.path(PIDS)

    @property
    def slides(self) -> str:
        return self.path(SLIDES)

    def slide(self, index: int) -> str:
        """Path for slide `index`, 1-based, zero-padded so names sort."""
        if index < 1:
            raise ValueError("slide index is 1-based, got %d" % index)
        return self.path(SLIDES, "slide-%03d.png" % index)

    def clear(self) -> None:
        """Remove the markers a previous run left.

        Only the fixed names above, never the directory: the caller may have
        put the deck or a fixture in here deliberately.
        """
        for name in (RESULT, PROGRESS, PIDS):
            target = self.path(name)
            if os.path.exists(target):
                os.remove(target)
