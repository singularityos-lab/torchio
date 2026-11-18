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

## Device- and environment-specific code

Code that exists only because of a specific device, architecture, firmware or environment must be marked on the line above it:

```
DEVICE-FIX(<scope>): <what breaks without this>
```

- `<scope>` is one of: `arm64`, `x86_64`, `uno-q`, `jetson`, `msm`, `nvidia`, `intel`, `amd`, `no-tpm`, `vm`, `u-boot`, `uefi`. A new scope is added to this list in the same pull request that first uses it.
- The text states what fails on that device without the code, not what the code does.
- Use the comment syntax of the file's language.
- This covers driver names, hardware probes, architecture constants, firmware quirks, boot-chain differences and anything gated on a specific environment.
- A pull request that adds or changes such code without the marker is not merged.
- `git grep -n "DEVICE-FIX("` lists every marker in the repository.
