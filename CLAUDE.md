@AGENTS.md

# CLAUDE.md, Torchio

Orientamento per le sessioni di lavoro su Torchio. Leggere prima di toccare il
codice.

## Cos'e'

Torchio e' il rimpiazzo di CUPS per l'ecosistema Singularity: un sistema di
stampa in Go, IPP-only, sandboxato (WASM), rootless, driverless. Greenfield,
iniziato il 2026-06-10.

## Regole non negoziabili (i cinque principi)

- **P1** IPP e' l'unico protocollo. Il core non ha il concetto di "driver":
  tutto e' un endpoint IPP con un URI.
- **P2** I trasformatori sono moduli WASM eseguiti con wazero in-process. Mai
  exec di filtri esterni. `CGO_ENABLED=0` sempre.
- **P3** Config dichiarativa (TOML, immutabile a runtime) piu' stato in SQLite
  (`modernc.org/sqlite`, puro Go). Il demone non riscrive mai la propria config.
- **P4** Rootless, per-utente. Ogni decisione di trust passa dal portale ush, mai
  PolicyKit. Vedi `docs/ECOSYSTEM.md`.
- **P5** Un binario statico multi-call (`cmd/torchio`), con symlink lp/lpr/lpstat.

## Costruire sopra go-foundation

Torchio si appoggia a `github.com/mirkobrombin/go-foundation` per tutta
l'infrastruttura applicativa (host, DI, FSM, eventi, config, worker, telemetria,
resiliency, health). PRIMA di scrivere infrastruttura, controllare se la lib la
offre gia': la mappatura e' in `docs/FOUNDATION.md`. Si consuma il modulo
pubblicato pinnato (`go-foundation v1.1.1`, niente `replace` locale).

## Convenzioni

- Module path `github.com/singularityos-lab/torchio`, Go 1.26.
- **Niente header di licenza per-file** (come go-foundation): i sorgenti iniziano
  col doc-comment del package o con `//go:build`. La licenza sta nel solo
  `LICENSE` (GPL-3.0-or-later, (C) 2026 Mirko Brombin).
- Niente em-dash/en-dash o frecce unicode nei file (un hook li blocca):
  punteggiatura ASCII e `->` semplice.
- README in inglese (stile go-foundation). Codice e commenti in inglese, doc di
  lavoro (PROGRESS/BACKLOG) in italiano.
- Niente blob binari committati: i moduli WASM di test si compilano on-demand
  dai sorgenti in `testdata/*/main.go` (vedi `*.wasm` in `.gitignore`).

## Layout

```
cmd/torchio/               entrypoint multi-call (P5) + shim lp/lpr/lpstat
cmd/torchio-fakeprinter/   stampante IPP finta ispezionabile (per i test)
internal/cli/              dispatch dei sottocomandi
internal/ipp/              client IPP (goipp)            [M0]
internal/detect/           sniffing del formato          [M0]
internal/model/            entita': MIME, Capabilities, ID(ULID), Job+FSM, Printer
internal/spool/            spool content-addressed SHA256 [M1]
internal/store/            SQLite (modernc.org/sqlite)    [M1]
internal/queue/            engine + attori per-printer + FSM [M1/M3]
internal/daemon/           torchiod (hosting go-foundation) [M1]
internal/control/          control plane JSON-RPC su UDS + SO_PEERCRED [M1]
internal/paths/            path XDG
internal/discovery/        mDNS read-only + recorder      [M2]
internal/portal/           bridge al broker ush (D-Bus)   [M2]
internal/printportal/      backend XDG org.freedesktop.impl.portal.Print [M2]
internal/doctor/           diagnostica (go-foundation health) [M2]
internal/transform/        grafo formati + planner Dijkstra + wazero + pdfium + PWG [M3]
internal/usb/              IPP-over-USB via usbfs puro Go  [M4]
internal/host/             driver legacy come WASM + manifest, server IPP [M5]
internal/share/            condivisione IPPS + responder DNS-SD [M6]
internal/scan/             client eSCL/AirScan            [M6]
internal/cupscompat/       front-end libcups sul socket CUPS (drop-in) [M7]
internal/fakeprinter/      logica della stampante finta di test
docs/                      ARCHITECTURE, FOUNDATION, ECOSYSTEM
```

## Roadmap e stato

**Roadmap M0-M7 COMPLETA e testata** (`go test -race ./...` verde, 17
pacchetti). Validato end-to-end su una **stampante Brother reale** (PDF
rasterizzato da pdfium in PWG-raster e stampato), e il drop-in CUPS (M7) sul
socket Unix con un client libcups-style. Dettaglio in `PROGRESS.md`; cosa resta
(fax, auth condivisione, config TOML, stream eventi, packaging buildroot) in
`BACKLOG.md`. Pubblicato come repo PRIVATO `github.com/singularityos-lab/torchio`
(branch `main`). M7: il daemon espone il front-end CUPS sul socket
(`$TORCHIO_CUPS_SOCKET` o `/run/cups/cups.sock`); cupsd non deve possederlo.

Sottocomandi: `print` (con `--queue` per lo spooler), `printer`, `daemon`,
`queue`, `discover`, `doctor`, `usb`, `host`, `share`, `scan`.

## Comandi utili

```
go build ./... && go test ./...
go run ./cmd/torchio                 # elenco sottocomandi
go run ./cmd/torchio-fakeprinter --help   # stampante finta per i test
```
