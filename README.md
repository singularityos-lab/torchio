# torchio

An IPP-only print system for Sinty OS that replaces CUPS. It answers the libcups IPP
dialect on the CUPS socket, so print dialogs, `lp`/`lpstat` and browsers keep working
against it unchanged. The spooler runs per user, format conversions (pdfium included)
run as WebAssembly modules, and printers found on the network are added only after the
user confirms.

## Build

    go build ./cmd/torchio

Static binary for the image:

    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o torchio ./cmd/torchio

## Use of Generative AI

Maintainers may use generative AI tools as assistants while working on torchio. Non-trivial assisted commits disclose the tool, model, and scope of the work.

AI tools may assist with code comments, documentation, repetitive code, and issue triage. Maintainers make project decisions and review every assisted change before it is merged.

Use these trailers for non-trivial assisted commits:

```plain
Assisted-by: <tool>:<model-version>
AI-Scope: <what the tool generated and the prompt or a short prompt summary>
```

Single-line completions, renames, and formatting changes do not need trailers.

Coding agents must also follow [AGENTS.md](AGENTS.md) before changing files,
creating commits, or opening pull requests.
