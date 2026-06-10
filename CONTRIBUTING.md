# Contributing to Torchio

## Quick start

```bash
git clone https://github.com/singularityos-lab/torchio
cd torchio
go build ./...
go test -race ./...
```

## Guidelines

- Keep the core IPP-only. Anything that is not an IPP endpoint (USB, legacy
  drivers, sharing) lives in a satellite that the core sees as an IPP printer.
- Format conversions run as WASM transforms; no native filters, no CGo.
- Discovery never creates printers. Every trust gesture goes through the
  `Portal` interface in `internal/portal`.
- Match the surrounding style. Comments explain why, not what.
- By submitting a contribution you agree to the [CLA](CLA.md).

## License

GPL-3.0-or-later.
