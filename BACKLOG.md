# Backlog di Torchio

Cose da fare, in un posto solo. Lo stato dettagliato dei milestone fatti e' in
`PROGRESS.md`; qui c'e' cosa MANCA.

## Stato milestone

| Milestone | Cosa | Stato |
|-----------|------|-------|
| M0 | client IPP, `torchio print`, `printer probe` | FATTO |
| M1 | spooler `torchiod` (SQLite, FSM, attori, control plane, CLI) | FATTO |
| M2 | discovery mDNS read-only, portale ush, backend `impl.portal.Print`, `doctor` | FATTO |
| M3 | transform engine (planner Dijkstra, immagine->PDF, runner wazero, **pdfium PDF->PWG raster**) | FATTO |
| M4 | `torchio-usb`: IPP-over-USB via usbfs puro Go (niente libusb), quirks DB | FATTO (I/O hardware da validare sulla stampante USB) |
| M5 | `torchio-host`: driver legacy come moduli WASM con manifest JSON | FATTO (manca un driver PCL/ESC-P di riferimento vero) |
| M6 | `torchio-share` (IPPS) + scansione eSCL | FATTO (manca il fax) |
| M7 | drop-in CUPS: front-end libcups (`internal/cupscompat`) sul socket CUPS | FATTO (validato su socket Unix con client libcups-style) |

**La roadmap M0-M7 e' completa.** Cosa resta sotto.

## PRIORITARIO: sessione di test end-to-end (su chiamata dell'utente)

L'utente vuole testare quando il sistema e' pronto. NON iniziare finche' non dice
"pronto".

**Fase 1, stampante fake controllabile. FATTA (2026-06-10).**
`cmd/torchio-fakeprinter` costruito e usato. Matrice di test eseguita con
processi reali (4 stampanti fake con formati diversi + daemon + `torchio print
--queue`), tutti gli scenari verdi:
- PDF -> stampante PDF: fast path, ricevuto PDF verbatim (niente rasterizzazione).
- PDF -> stampante solo-raster: **pdfium ha rasterizzato**, ricevuto PWG-raster
  (magic "RaS2"), 50649 byte.
- PNG -> stampante solo-PDF: immagine->PDF, ricevuto PDF.
- TXT -> stampante solo-PDF: nessun percorso, job **aborted**, niente inviato.
Aggiunto `torchio print --queue <printer>` (instrada nello spooler invece del
send diretto M0); era una lacuna reale.

Requisiti originali (per riferimento) di `cmd/torchio-fakeprinter`:
- Operazioni IPP con goipp: Get-Printer-Attributes, Validate-Job, Print-Job,
  Get-Job-Attributes, Cancel-Job. Risponde come una vera stampante.
- Configurabile: quali `document-format-supported` annunciare (per forzare fast
  path PDF / rasterizzazione PWG / immagini), stato, risoluzioni.
- Osservabile: salva ogni documento ricevuto su disco e logga formato, ticket e
  dimensioni, cosi' si vede ESATTAMENTE cosa arriva (es. PDF su stampante
  solo-raster deve arrivare come PWG-raster, magic "RaS2"; ri-renderizzare il
  raster in PNG per ispezione visiva).
- Flusso: fakeprinter, poi `torchio daemon`, poi `torchio printer add fake
  ipp://localhost:PORT/ipp/print`, poi `torchio print` di PDF/PNG/testo, poi
  ispezione. Coprire fast path PDF, immagine->PDF, PDF->PWG raster.
- Opzionale: annuncio mDNS per testare anche `torchio discover`.

**Fase 2, stampante reale in rete. FATTA (2026-06-10).** Brother vera
(10.30.10.33, A3 colore duplex, accetta urf/jpeg/pwg-raster/HBP ma NON pdf).
`printer add` + `doctor` ok; `torchio print --queue brother` di un PDF ->
pdfium ha rasterizzato in PWG-raster -> la Brother ha stampato una pagina
**leggibile**. L'encoder PWG fatto in casa confermato su hardware reale.
Nota: la stampante e' su 10.30.10.x (instradata via gateway), non sulla subnet
locale; mDNS non la vede da qui (isolamento client sul wifi), serviva l'IP.

**Fase 3 (se c'e' una stampante USB), validare M4.** `torchio usb ls`, poi
`torchio usb bridge`, poi `torchio printer add ... ipp://127.0.0.1:PORT/...`,
poi stampare. Qui si validano gli ioctl usbfs reali (claim, bulk, detach del
driver kernel) non testabili senza hardware; annotare i quirk del modello in
`data/usb-quirks.conf`. Rifiniture USB: confinamento landlock a `/dev/bus/usb` +
regola udev, hotplug verso il portale al primo uso, ZLP per i modelli che lo
richiedono, auto-registrazione presso il daemon.

## Funzionalita' rimaste (fuori dai milestone principali)

- **Fax** (era nel "poi eventualmente" di M6): mai implementato. Superficie
  ampia, uso raro. Da fare solo se serve davvero.
- **Auth della condivisione**: oggi `torchio share` espone IPPS senza
  autenticare i client. Aggiungere mTLS o credenziali (il doc lo prevede).
- **Driver di riferimento reale per M5**: un modulo WASM PCL5/ESC-P vero (oggi i
  test usano il modulo "upper" come prova del meccanismo).
- **Drop-in CUPS, deployment** (M7 e' fatto a livello di codice): bindare
  `/run/cups/cups.sock` in produzione (richiede dir scrivibile + cupsd non
  attivo: su Singularity non spedire cupsd, altrove `systemctl mask
  cups.socket cups.service`), prova con `lp`/`lpstat`/dialogo GTK veri,
  `CUPS-Get-PPD` esplicito, auth per le operazioni admin.
- **Ergonomia CLI**: i flag vanno PRIMA degli argomenti posizionali
  (comportamento standard del pacchetto `flag`); es. `torchio share --listen X
  nome`, non `torchio share nome --listen X`. Valutare un parser piu' tollerante.

- **Config TOML (P3).** Il demone oggi usa solo i path XDG; manca il caricamento
  di `/etc/torchio/config.toml` (default printer, log level, dpi rasterizzazione,
  ecc.) via `go-foundation/pkg/configuration`.
- **Stream eventi live verso il desktop.** L'event bus c'e' (i job emettono
  `JobEvent`), ma manca il metodo di streaming sul control plane a cui il
  desktop Vala/GTK si sottoscrive per il progresso job senza polling.
- **GC dello spool schedulato** + refresh periodico delle capability delle
  stampanti (`go-foundation/pkg/scheduler`).
- **Interop reale del Print portal** con xdg-desktop-portal + un'app GTK (il
  backend e' implementato e possiede il bus name; manca il test su sessione
  desktop vera).

## Transform: moduli e raffinamenti futuri

- **URF (image/urf, AirPrint)**: secondo encoder raster (stesso RLE packbits,
  header diverso da PWG). Molte stampanti Apple vogliono URF.
- **Ghostscript-WASM**: arco PostScript -> PDF, come modulo wazero.
- **littleCMS-WASM**: color management (ICC) invece di sRGB ovunque.
- **Parametri ai moduli WASM**: passare DPI/color-mode/ticket al modulo su un fd
  dedicato (oggi `WASMTransform.Run` passa solo stdin/stdout; pdfium usa un DPI
  fisso di 300 nel daemon).
- **Streaming tra hop**: oggi gli hop bufferizzano il documento in memoria (ok
  per pagine, da rendere streaming per job molto grandi).
- **Profilo "lite"**: mettere pdfium dietro un build tag per togliere i ~5MB di
  `pdfium.wasm` dall'immagine headless.

## Cancellazione

- La cancellazione M1 si applica ai job in coda; un job gia' in streaming verso
  la stampante non e' interrompibile a meta' invio (serve il lavoro di
  concorrenza di un milestone successivo, e Cancel-Job IPP verso la stampante).

## Packaging

- `package/torchio/torchio.mk` per singularity-os (buildroot), shim
  `lp`/`lpr`/`lpstat`, layout `/usr/share/torchio/transforms|drivers/*.wasm`,
  unit systemd utente (gia' in `data/systemd/torchiod.service`).
- Inizializzare il repo git e pubblicarlo su `singularityos-lab` (oggi la
  cartella non e' ancora un repo git).
