<div align="center">
  <h1>Torchio</h1>
  <p>A modern, IPP-only print system for Linux, in pure Go. A drop-in replacement for CUPS.</p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go" alt="Go 1.26+">
    <img src="https://img.shields.io/badge/cgo-disabled-success" alt="CGO_ENABLED=0">
    <img src="https://img.shields.io/badge/no-cups%20%7C%20libusb%20%7C%20avahi%20%7C%20poppler-success" alt="no native deps">
    <img src="https://img.shields.io/badge/license-GPL--3.0-blue" alt="GPL-3.0">
  </p>
</div>

---

Torchio is the print system of the [Singularity](https://github.com/mirkobrombin) desktop:
IPP-only, sandboxed, rootless, driverless. It does what CUPS does, the way a print
system would be designed today, in a single static Go binary with no native
dependencies (no CUPS, libusb, avahi, poppler or Ghostscript on the host).

It also speaks libcups on the CUPS socket, so existing apps (GTK and Qt print
dialogs, `lp`/`lpstat`, Chromium) use it without knowing CUPS is gone.

## Why

CUPS carries thirty years of design debt: a pipeline of exec'd native filters with
an implicit argv ABI, PPDs (a 1992 PostScript dialect), config the daemon rewrites
at runtime, a discovery daemon that *creates queues* from the network (the 2024 CVE
class), and a privileged monolith. Torchio inverts each point.

- **P1, IPP is the only protocol.** Every destination is an IPP endpoint with a URI.
  The core has no concept of a "driver".
- **P2, transformers are WebAssembly.** Every format conversion (PDF to raster,
  image to PDF, raster to PCL) is a WASM module run in-process with
  [wazero](https://github.com/tetratelabs/wazero): sandboxed by construction,
  architecture-independent, no CGo. pdfium itself runs this way.
- **P3, declarative config, state in a database.** Immutable TOML config plus SQLite
  (`modernc.org/sqlite`, pure Go) for state. The daemon never rewrites its own config.
- **P4, rootless and per-user.** The spooler runs in the user session with no
  capabilities; every trust decision goes to the [ush](https://github.com/mirkobrombin/ush)
  portal, never PolicyKit.
- **P5, one static multi-call binary.** `CGO_ENABLED=0`, busybox-style, with
  `lp`/`lpr`/`lpstat` shims.

## What it does

| Area | |
|------|--|
| **Print** | IPP/IPPS client; fast-path passthrough, or spooled with transforms and retries |
| **Spooler** | per-user `torchiod`: SQLite state, per-printer actor goroutines, an explicit job FSM, an append-only event log |
| **Rasterize** | PDF to PWG-raster via **pdfium compiled to WebAssembly** in a wazero sandbox, with an in-tree PWG-Raster encoder; native image to PDF |
| **Discover** | read-only mDNS/DNS-SD; a printer is added only after the user confirms through the portal |
| **Diagnose** | `torchio doctor`: end-to-end checks of the whole print path |
| **USB** | IPP-over-USB via usbfs directly (no libusb) |
| **Host drivers** | legacy printers as a WASM module plus a JSON manifest (the PPD's replacement), exposed as a local IPP printer |
| **Share** | re-expose a queue on the network over IPPS with a DNS-SD announcement |
| **Scan** | eSCL/AirScan client |
| **CUPS drop-in** | answers the libcups IPP dialect on the CUPS socket, so existing apps are unchanged |

## Quick start

```sh
go build ./...
go test ./...

# direct, no daemon
torchio printer probe ipps://printer.local/ipp/print
torchio print --to ipps://printer.local/ipp/print document.pdf

# through the spooler (transforms, retries)
torchio daemon &
torchio printer add office ipps://printer.local/ipp/print --default
torchio print --queue office document.pdf        # rasterizes if the printer needs it
torchio queue ls

# discovery, diagnostics, sharing
torchio discover
torchio doctor
torchio share office
```

## Architecture

The full design and the per-package map are in [`docs/`](docs/):

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md): data model, queue engine, transform
  engine, transport, discovery, security.
- [`docs/FOUNDATION.md`](docs/FOUNDATION.md): how each part maps onto
  [go-foundation](https://github.com/mirkobrombin/go-foundation), the zero-dependency
  base library Torchio is built on.
- [`docs/ECOSYSTEM.md`](docs/ECOSYSTEM.md): integration with the ush portal, the
  buildroot distro, and the desktop.

`PROGRESS.md` tracks what is done; `BACKLOG.md` tracks what is left.

## Status

The milestones M0 to M7 are implemented and tested (`go test -race ./...`). The
end-to-end path is validated against a real network printer: a PDF, sent to a printer
that does not accept PDF, is rasterized by pdfium and printed. What remains (fax, share
authentication, TOML config, a live desktop event stream, packaging) is in `BACKLOG.md`.

## License

GPL-3.0-or-later. Copyright (C) 2026 Mirko Brombin.

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
