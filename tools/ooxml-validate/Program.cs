// ooxml-validate — read a .pptx with Microsoft's own OOXML stack and report
// what it objects to.
//
// WHY THIS EXISTS, and what it can and cannot tell you.
//
// Four packaging defects shipped from this compiler, every one of them
// accepted by every reader available on the build host: python-pptx,
// LibreOffice, `unzip -t`, archive/zip's own reader. A desktop PowerPoint
// refused them. The gap was never the writing, it was that no reader on hand
// was as strict as the one that mattered.
//
// This is the closest free substitute: System.IO.Packaging is Microsoft's own
// OPC implementation and DocumentFormat.OpenXml is Microsoft's own Open XML
// SDK, so unlike LibreOffice this is not a reimplementation guessing at the
// same spec.
//
// It still is not PowerPoint, and the honest expectation is stated up front:
// the ZIP container defects -- a "version needed to extract" of 0, an
// all-zero MS-DOS date -- are advisory fields that .NET's own ZipArchive
// ignores, so this very likely will NOT reproduce them. What it does cover is
// a class nothing else here checks at all: whether every part validates
// against the PresentationML schema, and whether Microsoft's packaging layer
// can open the package in the first place.
//
// Exit codes:
//   0  Microsoft's stack opened it and the schema validator found nothing
//   1  it opened, but the schema validator reported errors
//   2  usage error
//   3  Microsoft's packaging layer REFUSED to open it -- the strongest signal
//      this tool can give, and the one worth acting on immediately

using System.Text.Json;
using DocumentFormat.OpenXml.Packaging;
using DocumentFormat.OpenXml.Validation;

internal static class Program
{
    private const int ExitClean = 0;
    private const int ExitSchemaErrors = 1;
    private const int ExitUsage = 2;
    private const int ExitUnopenable = 3;

    private static int Main(string[] args)
    {
        var json = args.Contains("--json");
        var paths = args.Where(a => !a.StartsWith("--", StringComparison.Ordinal)).ToArray();
        if (paths.Length != 1)
        {
            Console.Error.WriteLine("usage: ooxml-validate <file.pptx> [--json]");
            return ExitUsage;
        }

        var path = paths[0];
        if (!File.Exists(path))
        {
            Console.Error.WriteLine($"ooxml-validate: no such file: {path}");
            return ExitUsage;
        }

        PresentationDocument document;
        try
        {
            // Read-only: this must never be able to "helpfully" rewrite the
            // artifact it was asked to judge.
            document = PresentationDocument.Open(path, false);
        }
        catch (Exception ex)
        {
            // Microsoft's packaging layer refusing the file is the single most
            // valuable thing this tool can report, so it gets its own exit
            // code rather than being folded in with schema errors.
            Report(json, path, "unopenable", new[]
            {
                new Finding("opc", ex.GetType().Name, ex.Message, string.Empty),
            });
            return ExitUnopenable;
        }

        using (document)
        {
            var validator = new OpenXmlValidator();
            var findings = validator.Validate(document)
                .Select(e => new Finding(
                    "schema",
                    e.Id ?? string.Empty,
                    e.Description ?? string.Empty,
                    Describe(e)))
                .ToArray();

            Report(json, path, findings.Length == 0 ? "clean" : "schema-errors", findings);
            return findings.Length == 0 ? ExitClean : ExitSchemaErrors;
        }
    }

    // Describe locates a finding well enough to act on: the SDK gives the part
    // and an XPath, and without both a schema error is nearly unactionable in
    // a package with 180-odd parts.
    private static string Describe(ValidationErrorInfo error)
    {
        var part = error.Part?.Uri?.ToString() ?? string.Empty;
        var xpath = error.Path?.XPath ?? string.Empty;
        return string.IsNullOrEmpty(xpath) ? part : $"{part} {xpath}";
    }

    private static void Report(bool json, string path, string status, Finding[] findings)
    {
        if (json)
        {
            Console.WriteLine(JsonSerializer.Serialize(
                new { path, status, findings },
                new JsonSerializerOptions { WriteIndented = true }));
            return;
        }

        Console.WriteLine($"{path}: {status} ({findings.Length} finding(s))");
        foreach (var f in findings)
        {
            Console.WriteLine($"  {f.Kind} {f.Id}: {f.Description}");
            if (!string.IsNullOrEmpty(f.Where))
            {
                Console.WriteLine($"      at {f.Where}");
            }
        }
    }

    private sealed record Finding(string Kind, string Id, string Description, string Where);
}
