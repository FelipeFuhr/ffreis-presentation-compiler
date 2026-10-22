# ooxml-validate

Reads a `.pptx` with **Microsoft's own OOXML stack** —
`System.IO.Packaging` for the package and the Open XML SDK for the schema —
and reports what it objects to.

## Why, and exactly what it is worth

Four packaging defects shipped from this compiler, every one accepted by every
reader on the build host. A desktop PowerPoint refused them. The gap was never
the writing; it was that nothing available was as strict as the reader that
mattered.

This is the closest free substitute, because unlike LibreOffice it is not a
reimplementation guessing at the same spec — it is Microsoft's code.

**Its limits were measured, not assumed:**

| Fixture | Verdict |
| --- | --- |
| `[Content_Types].xml` removed | caught |
| invalid child element on a slide | caught, with part and XPath |
| required `cSld` missing | caught |
| **every zip version field set to 0** | **accepted** |

That last row is the whole caveat. It is the defect a desktop PowerPoint
rejected the real deck for, and Microsoft's own SDK opens it without
complaint — the field is advisory and .NET's `ZipArchive` ignores it just as
`zipfile`, `unzip`, LibreOffice and python-pptx did.

**So this is not a PowerPoint proxy.** It is a schema validator covering a
class nothing else here checks. The container invariants are held by
`internal/engine/pptx/verify.go`, which `Write` runs on its own output.

## Use

```bash
make ooxml-validate PPTX=dist/deck.pptx
```

Exit codes: `0` clean · `1` schema errors · `2` usage · `3` Microsoft's
packaging layer refused to open the file, which is the strongest signal this
tool can give.

`make doctor` reports whether `dotnet` is present. Install without root:

```bash
curl -fsSL https://dot.net/v1/dotnet-install.sh | bash -s -- --channel 8.0
export DOTNET_ROOT="$HOME/.dotnet" PATH="$HOME/.dotnet:$PATH"
```

The `DOTNET_ROOT` export is not optional — without it the built apphost
reports ".NET location: Not found" even with `dotnet` on `PATH`.

## Readers tested so far

Six, and only one is strict:

| Reader | zero zip version | shared theme part |
| --- | --- | --- |
| `zipfile` / `unzip -t` | accepted | n/a |
| python-pptx | accepted | accepted |
| LibreOffice | accepted | accepted |
| Go `archive/zip` | accepted | n/a |
| Open XML SDK (this) | accepted | accepted |
| **desktop PowerPoint** | **refused** | **repaired** |
