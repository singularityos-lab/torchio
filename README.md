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

