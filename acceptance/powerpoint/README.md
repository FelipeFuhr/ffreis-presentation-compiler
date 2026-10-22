# PowerPoint acceptance tests

Opens a compiler-built `.pptx` in **real desktop Microsoft PowerPoint** and
fails if PowerPoint refuses it, offers to repair it, or cannot draw its
slides.

## Why this exists

A `.pptx` this compiler produced was rejected by a desktop PowerPoint with
the generic "there was a problem with the content / repair?" prompt, while
every check available on the build host called it valid:

| Check | Verdict on the broken file |
| --- | --- |
| `zipfile.testzip()`, `unzip -t` | valid |
| CRC and size cross-check per entry | valid |
| `python-pptx` open | valid |
| LibreOffice round-trip (`make export-selftest`) | opens fine |
| current PowerPoint | opens fine |
| **the PowerPoint it was sent to** | **rejected the package** |

The defect was every ZIP entry declaring "version needed to extract" as
`0.0` — the Go zero value, written because `archive/zip`'s `CreateRaw` sets
only the header fields it is handed. That field is advisory, so every
lenient reader ignores it. A strict OPC parser does not.

**The lesson is not about that one field.** It is that this repo had no
reader strict enough to disagree with it, so an entire class of packaging
defect could ship. LibreOffice succeeding is not PowerPoint succeeding, and
neither is a green `go test`.

## Status

- **Implemented:** yes. The harness, negative control, diagnostics and the
  VM orchestration are complete, and the parts that can run on Linux are
  verified (fixture builders, the skip/fail gate, every `--dry-run` and
  error path of the host script).
- **Executed against Microsoft PowerPoint: no.** No licensed Windows or
  Office image exists here yet, so no run has ever reached a real
  PowerPoint. Until one does, **nothing in this repo may be described as
  PowerPoint-verified.**

## What a pass means, and what it does not

A pass means one PowerPoint version, on one Windows version, opened one
file without repairing it and rasterised every slide. It is not a statement
about any other version — which is why profiles are per-version and the
zip-version diagnostic records the installed build.

A **skip** means nothing at all. Set `POWERPOINT_ACCEPTANCE_REQUIRED=1` in
any run whose purpose is to verify PowerPoint, and absent prerequisites
become a failure instead.

## The one thing that actually blocks this

**Windows is easy to get; PowerPoint is not.** Both runtimes below give you a
Windows guest — the container one downloads its own media — and neither
installs Office. Windows on its own cannot answer the question this harness
exists for: the defects that shipped were rejected by *PowerPoint's* OPC
parser, and nothing else has one.

So Office must be installed into the guest once and kept, and that needs a
licence. No script can arrange that, however convenient the image is.

## Two runtimes

Chosen per profile with `RUNTIME=`:

| | `container` | `qemu` |
| --- | --- | --- |
| Setup | brings its own media | provision a VM yourself |
| Reproducible | manual Office state | snapshot restored per run |
| Cost | tens of GB | the image you already made |

**Watch where the disk goes.** `CONTAINER_STORAGE` is deliberately *not* the
container runtime's own store: that store is often on a cache tier with little
headroom — on the machine this was written on, podman's graph root sits on a
volume at 83% full — and a Windows install with Office is tens of gigabytes.
Point it at a disk with room.

## Running it

```bash
make pptx-acceptance PROFILE=win10-ppt
make pptx-acceptance PROFILE=win10-ppt PPTX=/path/to/deck.pptx SLIDES=34
scripts/powerpoint-acceptance.sh --profile win10-ppt \
  --pptx dist/deck.pptx --slides 34 --dry-run
```

With no `PPTX=`, the target builds `examples/sample-deck` first, so the
thing tested is this compiler's own current output.

Exit codes are deliberately distinct:

| Code | Meaning |
| --- | --- |
| 0 | PowerPoint opened it; every assertion passed |
| 1 | the test ran and failed — PowerPoint refused, or a dialog appeared |
| 2 | usage or profile error |
| 3 | infrastructure error — **the test did not run** |

### Directly on a Windows machine

```bat
python -m pip install -r requirements.txt
set POWERPOINT_ACCEPTANCE_REQUIRED=1
python -m pytest -q --pptx C:\path\to\deck.pptx --expect-slides 33
```

Close PowerPoint first. The harness refuses to run while any PowerPoint is
already open, rather than risk driving — and later terminating — a window
somebody was using.

## Provisioning a VM profile

One-time, per PowerPoint version. Images and installers stay out of git
(`.gitignore`), as does the profile itself; only `profiles/example.env` is
committed.

1. **Create the VM** with QEMU/KVM and install Windows normally. A GUI VM,
   not a Windows container: containers have no interactive desktop, so a
   modal repair prompt has nowhere to appear and the COM call that raised
   it never returns.
2. **Install desktop Microsoft PowerPoint** and complete first-run setup,
   including activation and dismissing every first-launch dialog. An
   unactivated Office shows modal nags that this harness correctly reports
   as failures.
3. **Enable autologon** so the VM boots to a logged-on interactive desktop.
   The host checks for an active console session and aborts with exit 3 if
   there is none.
4. **Install Python 3.10 or newer** — the pinned pytest requires it, so the
   VM must be Windows 10 or newer. That constrains the *Windows* version, not
   the PowerPoint version: old PowerPoint (2013/2016/2019) installs fine on
   Windows 10, and the PowerPoint version is what this measures. Tick "Add to
   PATH", then install the pinned dependencies:

   ```bat
   python -m pip install -r C:\ppt-acceptance\requirements.txt
   ```

5. **Enable the OpenSSH Server** optional feature, start the service, and
   authorise your key for `VM_SSH_USER`.
6. **Register the scheduled task** that runs the suite in the interactive
   session. `/IT` is the load-bearing flag — it is what makes the task run
   in the logged-on session rather than session 0:

   ```bat
   schtasks /Create /TN PptAcceptance ^
     /TR "C:\ppt-acceptance\run-tests.cmd" ^
     /SC ONCE /ST 00:00 /RU tester /IT /RL HIGHEST /F
   ```

7. **Shut the VM down** and take the snapshot the runs will reset to:

   ```bash
   qemu-img snapshot -c clean-provisioned /path/to/win.qcow2
   ```

8. **Write the profile:** copy `profiles/example.env` to
   `profiles/<name>.env` and fill it in.

The snapshot is restored before *and* after every run. Office is stateful —
one crash leaves recovery documents and a Document Recovery pane that
change what the next open does — so resetting is what makes two runs
comparable.

Keep a second profile matching the PowerPoint version that actually rejected
a file. An old PowerPoint tests old behaviour; only the version a reader
complained about can confirm a fix for that reader — and it can usually be
installed on a supported Windows, which is the cheaper way to get it.

## How it works

| File | Role |
| --- | --- |
| `scripts/powerpoint-acceptance.sh` | host: snapshot, boot, run, reset |
| `run-tests.cmd` | guest: the scheduled task's entry point |
| `conftest.py` | pytest options and fixtures |
| `prereq.py` | the skip/fail split, callable from any scope |
| `runner.py` | parent: deadline, dialog polling, guaranteed cleanup |
| `ppt_worker.py` | child: the COM calls that may block forever |
| `dialog_guard.py` | reads another process's dialogs; kills only our PIDs |
| `artifacts.py` | the one contained directory a run writes into |
| `fixtures.py` | builds known-bad packages from a known-good one |
| `pngcheck.py` | validates a slide export; stdlib, replaces Pillow |
| `test_portable.py` | tests for the three modules above that need no Windows |

Three rules in that code are load-bearing, not stylistic.

**The repair dialog is never answered.** `Open2007` is called with
`OpenAndRepair=msoFalse`, and a dialog that appears anyway is read and
reported, never clicked. Clicking Repair is how a malformed package becomes
a passing test.

**The COM calls run in a child process.** A modal dialog parks the calling
thread inside `Open2007` until a button is pressed, so the process that
opened the file cannot be the one that notices. The parent polls for
dialogs and owns the deadline.

**Only our own PowerPoint is ever terminated.** PIDs are captured by
diffing the running `POWERPNT.EXE` set across our own `DispatchEx`; nothing
is ever killed by image name.

**There is no Pillow.** It was carried for a screenshot and an
"is this PNG readable" check, and brought a dozen critical and high
advisories — heap overflows in the TIFF and JPEG decoders, an argument
injection into Ghostscript — for decoders this harness never touches.
Screenshots are GDI through `pywin32`; `pngcheck.py` validates exports with
the standard library, and does it *more* strictly than `Image.verify()`,
which checks chunk CRCs but never decompresses the pixel data. A slide
PowerPoint began writing and abandoned passed Pillow and fails this.

## Testing the orchestrator — this part does run here

```bash
make acceptance-selftest
```

32 assertions over `scripts/powerpoint-acceptance.sh` with the guest faked:
`podman`, `ssh`, `scp`, `qemu-img` and `qemu-system-x86_64` are replaced with
scripts that log every invocation and answer the way a real guest would.

The orchestrator was the part of this harness that had never been exercised,
because exercising it needed the very thing that is missing. That left its
whole sequence unverified — including **which exit code it reports**, which is
the worst thing here to get wrong: it is the difference between "PowerPoint
refused the file" and "we never asked PowerPoint". The selftest pins exactly
that: a guest exit of 1 must surface as 1 with "RAN and FAILED" and must
*not* say "DID NOT RUN", while a missing runtime must be 3 and must say it.

It found a real bug on first run. Runtime dispatch was written
`[[ $RUNTIME == qemu ]] && qemu_alive || container_alive`, which runs the
third branch whenever the second is false — so every time the VM was found
dead, the orchestrator asked the *container* whether it was running and
carried on.

What it does not test is Windows, which no amount of faking stands in for.

## Testing the harness itself

```bash
make acceptance-test          # from the repo root; no Windows needed
```

27 tests over `fixtures`, `pngcheck` and `artifacts` — the three modules that
decide whether a PowerPoint result means anything. If `fixtures` quietly
stopped breaking the negative control, or `pngcheck` stopped rejecting a
half-written export, the suite would go green while measuring nothing.

They are hermetic: every input is built in-process, so there is no compiler,
Chrome, PowerPoint or network involved. The Windows-only tests skip, and the
target says so rather than printing an unqualified "ok".

`make acceptance-test` **fails** when `uv` is absent rather than skipping —
deliberately unlike `make export-selftest`, which `exit 0`s without
LibreOffice and is the reason a broken package shipped looking checked.

It is not in CI: this repo's pipeline has no Python tier, and its Sonar job is
a pinned reusable Go workflow. That is also why `sonar-project.properties`
excludes this directory from coverage — the scan ingests a Go profile only, so
every Python line would read as uncovered however well tested. The properties
file records both.

## The negative control

`test_the_harness_detects_a_package_powerpoint_must_reject` strips
`[Content_Types].xml` from the real artifact and requires PowerPoint to
refuse it. Without that, a harness that had silently stopped observing
would look exactly like a run of clean passes — the same reason this repo
ships `check-selftest` and `validate-selftest`.

If that test ever fails, **every pass in the file is void** until it is
explained.

## Font embedding: the first thing to test here

`-pptx-embed-fonts` writes plain sfnt into `ppt/fonts/*.fntdata`. PowerPoint
itself writes EOT there. **Which one PowerPoint will read is unknown**, and it
cannot be determined without a PowerPoint: LibreOffice ignores embedded
`.pptx` fonts regardless of format — verified against a real file carrying
Microsoft's own EOT fonts for a typeface not installed on the test machine,
which it substituted anyway.

So when a licensed PowerPoint is available, this is the highest-value thing to
point it at:

1. Build with `-pptx-embed-fonts` and a typeface the VM does **not** have
   installed, so substitution is detectable.
2. Open it and check whether the text renders in that face.
3. If it substitutes, the format is wrong and EOT is the next thing to try —
   and at that point there is finally an oracle to develop it against.

Until then, treat the typeface named in a deck as the thing that matters, and
read the font warning every build prints.

## The zip-version diagnostic

`test_report_whether_this_powerpoint_rejects_zip_version_zero` rebuilds the
original defect — all version fields back to `0.0`, every part otherwise
byte-identical — and *records* whether this PowerPoint rejects it, writing
`zip-version-zero-report.json`.

It reports rather than asserts because the field is advisory: a reader is
within its rights either way, so the answer describes the installed
PowerPoint, not the compiler. Run across profiles, it is what turns "a
reader could not open it" into "PowerPoint *x.y* rejects this field".

## CI

`.github/workflows/powerpoint-acceptance.yml` is `workflow_dispatch` only
and takes the runner label as an input, with no default. That is
deliberate: no runner in this fleet carries a licensed PowerPoint, and a
workflow that silently queues forever against a label nobody serves is
worse than no workflow — it looks like a gate while gating nothing.

GitHub's hosted Windows runners do **not** include Office, and they have no
interactive desktop session, so they cannot run this.
