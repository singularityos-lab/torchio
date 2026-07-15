# Progress log

Registro di avanzamento per la review. Aggiornato man mano. Le scelte non ovvie
sono annotate con "Nota".

## M0, client IPP e print (COMPLETO)

Stato: implementato e testato. `go test ./...` verde, `go vet` pulito,
`gofmt` pulito.

Cosa c'e':

- `internal/ipp`: client IPP completo sopra `goipp` + `net/http`.
  - `Client` con `GetPrinterAttributes`, `ValidateJob`, `PrintJob`,
    `GetJobAttributes`, `CancelJob`.
  - `uri.go`: normalizzazione URI (ipp->http, ipps->https, porta 631 di
    default, path /ipp/print di default).
  - `attr.go`: reader tipati sugli attributi IPP di risposta.
  - `status.go`: `StatusError` strutturato, distinzione success/error sui
    codici IPP.
  - `capabilities.go`: `ParseCapabilities` da Get-Printer-Attributes a
    `model.Capabilities`.
  - Test: finto server IPP via httptest che decodifica le richieste; verifica
    del wire format di Print-Job (documento concatenato al messaggio, non
    multipart), parsing capabilities, StatusError, Get-Job/Cancel.
- `internal/model`: `MIME` e `Capabilities` (puro, niente dipendenze da
  trasporto o storage).
- `internal/detect`: sniffing del formato da magic bytes con reader che
  ripropone l'intero stream (PDF, PostScript, JPEG, PNG, PWG-raster, URF,
  testo). Test inclusi.
- `internal/cli`: `print` e `printer probe` reali.
  - `print`: sniff -> Get-Printer-Attributes -> controllo formato (fast path
    M0, niente transform) -> Validate-Job -> Print-Job. Legge da file o stdin.
  - `printer probe`: dump leggibile delle capability (il comando che CUPS non
    ha mai avuto).

Note di design M0:

- TLS con `InsecureSkipVerify`: in Torchio la fiducia IPPS si stabilisce via
  TOFU e portale ush, non via CA pubbliche. Il pinning arriva con lo store
  (M1/M2). Marcato `//nolint:gosec`.
- Senza transform engine (M3), `print` su un formato non supportato dalla
  stampante RIFIUTA invece di rasterizzare, oppure procede con `--force`.

## M1, lo spooler torchiod (COMPLETO)

Stato: implementato, testato (`go test -race ./...` verde), e funzionante come
binario reale end-to-end. ~4800 righe di Go totali.

Cosa c'e':

- `internal/model`: entita' complete. `id.go` implementa ULID in-tree (zero
  dipendenze): 48 bit di timestamp + 80 random, base32 Crockford,
  lessicograficamente ordinabile, con `ParseID` inverso. `job.go` definisce la
  FSM dei job (stati 1:1 con IPP `job-state`) come tag dichiarativo.
- `internal/spool`: blob store content-addressed SHA256, scrittura atomica
  (temp + fsync + rename + fsync dir), dedup, `Verify` per `doctor --fsck`.
- `internal/store`: SQLite puro Go (`modernc.org/sqlite`), WAL + foreign keys +
  busy_timeout via DSN. CRUD per printer/job, event log append-only con seq
  auto-incrementale, cache effimera `devices_seen`, cascade delete.
- `internal/queue`: il cuore. Engine + attori per-printer (una goroutine per
  stampante, serialita' per coda, parallelismo tra code, niente lock
  condivisi). FSM guidata da `go-foundation/pkg/fsm/machine`. Percorso job:
  created->validating->pending->spooled->sending->completed, con abort sui
  formati non supportati (niente transform in M1) e retry resiliente
  (`go-foundation/pkg/resiliency`) sugli errori transitori, abort immediato
  sugli StatusError IPP permanenti. Recovery al riavvio (sending->pending).
- `internal/daemon`: wiring di torchiod (store + spool + bus + engine + control
  server), shutdown ordinato, basato su go-foundation (logger, events).
- `internal/control`: control plane su Unix domain socket. JSON-RPC
  (net/rpc/jsonrpc) con autenticazione SO_PEERCRED (solo stesso utente, P4).
  Metodi: AddPrinter (con probe capability), ListPrinters, RemovePrinter,
  SetDefault, Submit, ListJobs, CancelJob, JobHistory.
- `internal/paths`: risoluzione path XDG (stato in XDG_STATE_HOME, socket in
  XDG_RUNTIME_DIR).
- `internal/cli`: `daemon`, `printer add|ls|rm|default|probe`, `queue
  ls|cancel|history` come client del demone. SIGINT+SIGTERM -> shutdown pulito.
- `data/systemd/torchiod.service`: unit utente.

Verificato a mano col binario reale: avvio demone, socket 0600 creato, CLI che
parla via UDS, add con probe che fallisce in modo pulito su host irraggiungibile,
SIGTERM che pulisce il socket. Il test `internal/control` esercita l'intero
stack (RPC reale su socket + probe IPP finto + submit + completamento + history).

### BUG trovato in go-foundation (RISOLTO a monte, v1.1.1)

`pkg/fsm/machine.New` usava `meta.Index` (un contatore dei soli campi *taggati*)
come se fosse l'indice del campo nella struct, in `elem.Field(meta.Index)`.
Funzionava solo quando il campo con tag `fsm` era il PRIMO campo della struct
(come in tutti i test di go-foundation). Con `model.Job`, dove `State` e' il
quarto campo, la macchina agganciava per errore il campo `ID`.

Fix: in go-foundation `machine.New` ora risolve il campo per nome
(`FieldByName`/`FieldByIndex`), rilasciato in **v1.1.1** (commit `50e1d7e`,
release creata con `gh`). Torchio ora consuma `go-foundation v1.1.1` dal proxy
(niente piu' `replace` locale) e la FSM e' agganciata direttamente a `model.Job`
(tag unico su `model.Job.State`); il workaround col carrier mono-campo e' stato
rimosso. Il test `internal/queue.TestJobBindsToFSM` esercita il fix dal consumer
reale (campo `State` non primo).

### Limitazioni note di M1 (oneste)

- La cancellazione si applica ai job in coda; un job gia' in streaming verso la
  stampante non e' interrompibile a meta' invio (serve il lavoro di concorrenza
  di un milestone successivo).
- Il documento viaggia inline (base64) nella RPC Submit; lo streaming e' una
  ottimizzazione successiva.
- Niente ancora: caricamento config TOML (`/etc/torchio/config.toml`, P3), stream
  eventi live verso il desktop (il bus c'e', manca il metodo di streaming),
  refresh periodico delle capability, GC dello spool schedulato.

## M2, discovery + portale + doctor (COMPLETO, incluso il backend Print portal)

Stato: implementato e testato (`go test -race ./...` verde, ~6200 righe).

Cosa c'e':

- `internal/discovery`: discovery mDNS/DNS-SD **pura Go** (niente avahi), tramite
  `golang.org/x/net/dns/dnsmessage` + multicast UDP stdlib. `assembler.go` e' il
  cuore testabile (assembla PTR/SRV/TXT/A in `Service` da uno o piu' pacchetti,
  decodifica gli escape DNS-SD); `mdns.go` e' il browser live; `recorder.go`
  scrive in `devices_seen` (read-only). **Per costituzione la discovery non crea
  MAI una stampante**: riempie solo la cache effimera. Test: assembler su un
  messaggio mDNS reale + recorder che verifica "zero printer creati".
- `internal/portal`: bridge al broker ush (P4). Interfaccia `Portal`
  (`RequestPermission`), impl D-Bus (`godbus`) verso
  `io.github.mirkobrombin.ush.Broker1.RequestPermission`, piu' i fake
  `AutoApprove`/`AlwaysDeny`. Decisioni allow/allow_session/allow_always/deny.
- `internal/control`: `ListDiscovered` + `AddDiscovered`. **AddDiscovered e'
  l'unico punto in cui la discovery puo' portare a una modifica di config, e
  passa SEMPRE dal portale**: solo l'approvazione crea la stampante con
  `Origin: discovered+confirmed`. Test: approvato -> stampante creata; negato ->
  niente creato (caso critico per la sicurezza).
- `internal/doctor`: diagnostica end-to-end su `go-foundation/pkg/health`. Check
  del demone (socket) e di ogni stampante (Get-Printer-Attributes live, che
  esercita DNS/TCP/handshake TLS/IPP). Exit 1 se qualcosa non passa.
- `internal/daemon`: sceglie il portale (D-Bus se c'e' session bus, altrimenti
  **deny-by-default**) e avvia il recorder di discovery (fallimento del socket
  multicast non-fatale).
- CLI: `discover` (ls + `add <key> <name>` con conferma via portale), `doctor
  [uri]`.

Verificato a mano col binario: `doctor` con demone su (daemon ok), `doctor
<uri-irraggiungibile>` (printer FAIL con causa), `discover` read-only, exit code
corretti.

### Backend `org.freedesktop.impl.portal.Print` (FATTO)

`internal/printportal`: backend XDG che fa stampare le app GTK sandboxate
attraverso Torchio. Logica pura (conversione GtkPrintSettings -> JobTicket,
handler PreparePrint/Print, token store mono-uso) separata dal wiring D-Bus
(`dbus.go`, godbus) per testabilita'. PreparePrint risolve stampante (settings o
default) e ticket e rilascia un token; Print riceve il documento come file
descriptor, legge e lo invia allo spooler. Il daemon lo espone sul session bus
(non-fatale se assente). Test: conversione settings, flusso prepare->print con
token, fallback al default, errore senza stampante. L'export D-Bus e' validato
in pratica: avviando il daemon su un session bus reale possiede il nome
`org.freedesktop.impl.portal.desktop.torchio` ("XDG print portal active").
Quello che resta non validabile qui e' l'interop end-to-end con
xdg-desktop-portal + un'app GTK (serve una sessione desktop).

## M3, transform engine (COMPLETO, pdfium incluso)

Stato: implementato e testato (`go test -race ./...` verde, ~7400 righe).

Cosa c'e' (`internal/transform`):

- **Grafo dei formati + planner Dijkstra.** `Transform` = un arco (`Edge{From,
  To, Cost}`) + `Run(in, out, params)`. La `Registry` fa Dijkstra dal formato
  del documento all'insieme di formati accettati dalla stampante, scegliendo il
  percorso piu' economico. Costo pesato sulla fedelta' (passthrough = piano
  vuoto = fast path, niente rasterizzazione). Test: percorso piu' economico,
  fast path, nessun percorso, target piu' vicino.
- **Trasformatore nativo immagine -> PDF** (puro Go): JPEG/PNG incapsulati in un
  PDF a una pagina (XObject DeviceRGB FlateDecode). Permette di stampare
  un'immagine su una stampante solo-PDF senza pdfium. Test: produce un PDF
  valido (header/xref/trailer, riconosciuto da `detect`).
- **Runner wazero per moduli WASM** (P2): `WASMTransform` esegue un modulo WASI
  (stdin->stdout) in sandbox (niente fs/rete/clock/syscall), arch-indipendente,
  ri-istanziabile per job. Testato **davvero** con un modulo wasip1 reale
  (`testdata/upper`, sorgente Go compilato a wasm) eseguito via wazero, e
  composto nel planner come un trasformatore qualunque.
- **pdfium (PDF -> PWG raster), il problema duro, RISOLTO.** `internal/transform/
  pdfium.go` rasterizza i PDF con **pdfium compilato a WASM eseguito in wazero**
  (via `github.com/klippa-app/go-pdfium`, backend webassembly, blob `pdfium.wasm`
  ~5MB embeddato nel modulo, niente CGo). Gira **in sandbox senza filesystem**
  (FSConfig vuoto): pdfium parsa l'input ostile (PDF malformato) senza syscall.
  Init pigro (compila il wasm solo al primo PDF da rasterizzare). `internal/
  transform/pwgraster.go` e' l'encoder PWG-Raster (PWG 5102.4): sync word, header
  di pagina 1796 byte, dati con RLE packbits a livello di pixel (stile CUPS/URF).
  **Blindato con round-trip** (encoder + decoder che concordano su solid,
  gradiente, pseudo-random, strisce sottili) piu' verifica dell'header.
- **Integrazione nella coda.** Lo stage `pending` pianifica: piano vuoto -> fast
  path (`pending->spooled`); piano non vuoto -> `pending->transforming`, esegue
  gli hop (legge dallo spool, esegue, ri-spoola il risultato content-addressed,
  aggiorna `job.Document`), poi `->spooled`. `validate` abortisce solo se NON
  esiste alcun percorso. Il daemon registra pdfium nella registry.
  Test end-to-end: (1) un PNG -> stampante solo-PDF viene trasformato e spedito
  come PDF; (2) **un PDF -> stampante solo-raster viene rasterizzato da pdfium e
  spedito come PWG-raster** (verificato il magic "RaS2" sui byte spediti). Tutto
  verde anche con `-race`.

### Estensioni future (non bloccanti)

- Passare i parametri (DPI dalle caps/ticket, color mode) al rasterizzatore in
  modo piu' fine; oggi pdfium usa un DPI fisso (300 nel daemon).
- URF (image/urf, AirPrint) come secondo encoder raster (stesso RLE, header
  diverso). Ghostscript-WASM (PostScript -> PDF) e littleCMS (color management)
  come ulteriori moduli, con lo stesso runner.
- Buffering: gli hop bufferizzano il documento in memoria (ok per pagine, da
  rendere streaming per job molto grandi).
- Profilo "lite" senza rasterizzazione: mettere pdfium dietro un build tag per
  togliere i ~5MB di wasm dall'immagine headless.

## M4, torchio-usb (IPP-over-USB) (CORE COMPLETO, I/O hardware da validare)

Stato: implementato e testato (le parti testabili senza hardware). `internal/usb`:

- **Parsing descrittori USB** (`descriptor.go`, puro): trova le interfacce
  IPP-over-USB (classe 7/1/4) e i loro endpoint bulk IN/OUT da device e config
  descriptor. Test con descrittori costruiti a mano (incl. casi: piu' interfacce,
  endpoint mancante, endpoint non-bulk).
- **Quirks DB** (`quirks.go`, puro): file di testo INI-like (`[VVVV:PPPP]` con
  wildcard, key=value), lookup con merge specifico-su-wildcard. Tiene le
  perversioni dei firmware fuori dal codice. Test inclusi. Default in
  `data/usb-quirks.conf`.
- **Bridge HTTP<->USB** (`bridge.go`): espone una stampante USB come endpoint IPP
  su `127.0.0.1:PORT` (P1: il core la vede come una stampante di rete). La parte
  USB sta dietro l'interfaccia `Transport`/`Conn`, quindi il tunnel e' testato a
  fondo con un finto printer in-memory: **un vero scambio IPP (goipp) attraversa
  il bridge** (client IPP -> TCP -> tunnel -> stampante IPP simulata), oltre a un
  round-trip HTTP. Test verdi anche con `-race`.
- **usbfs reale** (`usbfs_linux.go`, build tag linux): apre `/dev/bus/usb/BBB/DDD`,
  legge i descrittori, stacca il driver kernel (usblp) e claima le interfacce,
  trasferimenti bulk via ioctl `USBDEVFS_*` (niente libusb, niente cgo). Pool di
  interfacce con lease per connessione. La logica pura (`parseNode`) e' testata;
  **gli ioctl I/O richiedono una stampante USB vera** (si validano nella sessione
  di test del backlog).
- **Enumerazione** (`enumerate_linux.go`): scan read-only di `/dev/bus/usb`,
  trova le stampanti IPP-over-USB senza disturbarle.
- **CLI**: `torchio usb ls` (elenca) e `torchio usb bridge [--quirks file]`
  (espone ogni stampante USB come `ipp://127.0.0.1:PORT`, da aggiungere poi con
  `torchio printer add`). `usb ls` gia' provato: scansiona usbfs reale.

Da rifinire (non bloccante): confinamento landlock a `/dev/bus/usb` + regola
udev; hotplug che genera una richiesta al portale al primo uso di un device;
ZLP per i quirk che lo richiedono; auto-registrazione delle stampanti USB presso
il daemon. La validazione su hardware vero e' parte della sessione di test
(`BACKLOG.md`).

## M5, torchio-host (driver legacy come moduli WASM) (COMPLETO)

Stato: implementato e testato (`go test -race ./...` verde). `internal/host`
ospita un driver legacy (modulo WASM + manifest JSON) e lo espone come stampante
IPP locale, nello spirito di PAPPL. E' il primo componente in cui Torchio fa da
**server IPP** (finora solo client).

- **Manifest** (`manifest.go`): il sostituto del PPD. JSON tipato e validato
  (`name`, `accepts`, `produces`, `capabilities`, `quirks`), niente PostScript.
  Si traduce in attributi IPP standard (`IPPCapabilities`). Esempio in
  `data/drivers/generic-pcl5.json`. Test parse + validazione (campi mancanti,
  campi sconosciuti).
- **Server IPP** (`ippserver.go`): handler HTTP lato stampante. Risponde a
  Get-Printer-Attributes (dalle capability del manifest), Validate-Job,
  Print-Job (legge il documento, lo passa al driver, scrive al backend,
  ritorna job-id/state), Get-Job-Attributes, Cancel-Job. **Il client IPP vero
  di Torchio (`internal/ipp`) lo interroga**, quindi il giro client<->server e'
  testato davvero.
- **Pipeline driver**: il driver e' un modulo WASM (riusa il runner wazero di
  M3) con edge `accepts -> produces` (es. `image/pwg-raster -> vendor/pcl5`).
  Sandboxato, arch-indipendente, installabile per-utente senza root.
- **Backend di output** (`backend.go`): dove vanno i byte in linguaggio
  stampante. Interfaccia con impl `BufferBackend` (test), `FileBackend`
  (device node, es. `/dev/usb/lp0`), `SocketBackend` (JetDirect/AppSocket 9100).
- **Host** (`host.go`): lega manifest + driver + backend + server IPP su
  `127.0.0.1:PORT`. Il core ci stampa come su una stampante IPP qualunque; con
  M3 il PDF viene prima rasterizzato a PWG, poi il driver lo converte nel
  linguaggio della stampante.
- **CLI**: `torchio host --manifest d.json --module d.wasm --backend
  socket://host:9100|file:/dev/usb/lp0|discard`.

Test end-to-end: (1) IPP client -> host server con un driver in-Go -> backend
riceve l'output convertito; (2) **driver WASM reale** (modulo upper) attraverso
l'intera catena IPP client -> server IPP -> driver wazero -> backend. Provato
anche col binario: `torchio host` espone il driver e `torchio printer probe` ne
legge le capability via IPP reale.

Da rifinire (non bloccante): un driver di riferimento PCL/ESC-P vero (oggi il
test usa un modulo "upper" come prova del meccanismo); piu' attributi IPP per
piena interop IPP-Everywhere; integrazione coi quirk del manifest.

## M6, torchio-share + scansione (COMPLETO)

Stato: implementato e testato. La roadmap M0-M6 e' completa.

- **torchio-share** (`internal/share`): ri-espone una stampante locale in rete su
  **IPPS** (TLS, niente IPP in chiaro) e la annuncia via DNS-SD, inoltrando i job
  allo spooler (proiezione della coda, non duplicato). Riusa il server IPP di M5
  (`host.Server`): una coda ospitata e una condivisa sono entrambe "rispondi a
  IPP, accetta job". Certificato self-signed generato al volo (`tls.go`,
  fiducia via TOFU/portale, non CA pubbliche). **Responder DNS-SD**
  (`responder.go`): risponde alle query mDNS per il servizio condiviso con
  PTR/SRV/TXT/A e annuncia periodicamente (il contrario del browser read-only di
  M2). Test: server IPPS interrogato dal client IPP vero su TLS + inoltro del
  documento; costruzione dei record del responder.
- **Scansione eSCL/AirScan** (`internal/scan`): client eSCL (gli scanner sono
  stampanti al contrario sullo stesso mDNS). `Capabilities` e `Scan` (POST
  ScanJobs, segue la Location, GET NextDocument). Test con un finto scanner HTTP.
- **CLI**: `torchio share <printer>` (IPPS + annuncio), `torchio scan --from
  <eSCL-url> --out file`.

**Smoke full-stack con soli binari torchio**: `torchio host` (stampante IPP vera
con driver WASM) -> il daemon la aggiunge via probe IPP -> `torchio share` la
ri-espone su IPPS -> `torchio printer probe ipps://...` legge le capability su
TLS -> `torchio print --to ipps://...` stampa attraverso la condivisione ->
daemon -> spooler -> host -> driver WASM -> job **completed**. L'intera pila
(M0+M1+M5+M6) validata con processi reali che parlano IPP/IPPS tra loro.

Da rifinire (non bloccante): fax (raramente usato, superficie ampia, in
backlog); mTLS/credenziali per autenticare i client della condivisione (oggi
IPPS senza auth client); piu' attributi IPP-Everywhere; il fatto che i flag
della CLI vanno prima degli argomenti posizionali (comportamento standard di
`flag`, vedi backlog).

## Sessione di test, Fase 1 (FATTA, 2026-06-10)

Costruito `cmd/torchio-fakeprinter` (`internal/fakeprinter`): una vera stampante
IPP-Everywhere su localhost, configurabile (quali formati annunciare) e
ispezionabile (salva ogni documento ricevuto e logga formato dichiarato vs
**formato realmente rilevato** sui byte). Aggiunto `torchio print --queue
<printer>` per instradare nello spooler (la lacuna che impediva di testare il
transform engine dalla CLI).

Matrice eseguita con processi reali (4 fakeprinter + daemon), tutta verde:

| Scenario | Stampante accetta | Inviato | Ricevuto davvero | Job |
|---|---|---|---|---|
| PDF -> PDF | pdf, pwg | PDF | application/pdf (verbatim, fast path) | completed |
| PDF -> raster | solo pwg-raster | PDF | image/pwg-raster ("RaS2", pdfium ha rasterizzato) | completed |
| PNG -> PDF | solo pdf | PNG | application/pdf (immagine->PDF) | completed |
| TXT -> PDF | solo pdf | TXT | niente (nessun percorso) | aborted |

I file salvati su disco confermano i byte reali (%PDF / RaS2).

## Sessione di test, Fase 2 (FATTA, 2026-06-10): STAMPANTE REALE

Testato contro una **Brother vera** in rete (10.30.10.33, A3 colore duplex).
Capability lette via IPP: accetta `image/urf, image/jpeg, image/pwg-raster,
application/vnd.brother-hbp` ma **NON application/pdf** -> il caso perfetto per
la rasterizzazione M3.

Stampato un PDF reale via `torchio print --queue brother`. Storico job:
created->validating->pending->**transforming** ("1-hop transform to
image/pwg-raster", pdfium ~3s)->spooled->sending->**completed** (accepted by
printer). **La pagina e' uscita fisicamente ed e' leggibile.**

Significato: l'intera pila validata su hardware reale (M0 client IPP, M1
spooler/FSM/attori, M3 transform + pdfium-in-wazero + **l'encoder PWG-raster
scritto in casa**). Una Brother fisica ha accettato e stampato il PWG-raster
generato da `pwgraster.go`: la parte piu' rischiosa del progetto, confermata sul
campo, non solo in round-trip.

Resta (con l'utente, opzionale): tuning fine raster->stampante (DPI/margini/
color) se necessario su altri modelli; Fase 3 USB se c'e' una stampante USB.

## M7, drop-in di CUPS (COMPLETO)

`internal/cupscompat`: fa vedere Torchio alle app esistenti come se fosse
`cupsd`, senza modifiche alle app. Parla il dialetto IPP di libcups sul socket
CUPS: operazioni `CUPS-Get-Printers`/`CUPS-Get-Default` (l'enumerazione che usa
`cupsGetDests`), `Get-Printer-Attributes`, `Print-Job`, **`Create-Job` +
`Send-Document`** (il flusso del dialogo GTK/Qt via libcups), `Get-Jobs`,
`Get-Job-Attributes`, `Cancel-Job`. Le code che annuncia sono quelle dello
store; i job vanno allo spooler (transform inclusi). Mappa job-id interi CUPS
<-> ULID dello spooler, e gli stati Torchio sugli enum IPP job-state. Risponde
con gli attributi che libcups si aspetta (printer-uri-supported in forma
`ipp://localhost/printers/NAME`, printer-type bitmask, device-uri, ecc.).

Il daemon lo espone sul socket CUPS (`$TORCHIO_CUPS_SOCKET` o
`/run/cups/cups.sock`), non-fatale se non riesce a bindare. Deployment: cupsd
non deve possedere il socket (su Sinty OS non si spedisce cupsd; altrove
`systemctl mask cups.socket cups.service`).

Validato: test unit con richieste IPP-CUPS reali, e **smoke sul socket Unix
vero** con un client libcups-style (`CUPS-Get-Printers` -> ha restituito la
Brother reale come coda CUPS con la sua device-uri). Quindi un `lpstat -p`, il
dialogo GTK/Qt o Chromium vedrebbero le code di Torchio senza saperlo.

Da rifinire: prova con i CLI CUPS veri (`lp`/`lpstat`) quando installati; bind a
`/run/cups/cups.sock` in produzione + masking di cupsd; CUPS-Get-PPD (oggi
risponde PPD-less/IPP-everywhere implicitamente).

## ROADMAP COMPLETA (M0-M7)

Tutti i milestone principali sono fatti. Cio' che resta (in `BACKLOG.md`): la
sessione di test end-to-end con stampante fake e reale (FATTA), fax, config
TOML, stream eventi live verso il desktop, packaging buildroot, e le rifiniture
annotate per ogni milestone.
