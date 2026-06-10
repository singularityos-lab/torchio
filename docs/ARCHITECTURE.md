# Architettura di Torchio

Versione di lavoro. La fonte concettuale e' il design v1 ("Torchio, architettura
di un sistema di stampa moderno in Go"); questo documento ne e' la sintesi
operativa, allineata a go-foundation e all'ecosistema Singularity.

## 0. Diagnosi: perche' CUPS e' fatto male

Ogni difetto di CUPS diventa un principio invertito in Torchio:

1. Pipeline a processi exec-utati, ABI implicita fatta di argv posizionali e
   variabili d'ambiente magiche, zero tipi, zero sandbox, zero versioning.
2. PPD, formato di descrizione printer del 1992 in dialetto PostScript.
3. Configurazione sparpagliata e riscritta dal demone stesso a runtime.
4. cups-browsed: discovery con potere di scrittura sulla config (CVE 2024).
5. Monolite con privilegi: cupsd storicamente root, filtri con ambiente enorme.
6. Driver come pacchetti di sistema arch-dipendenti, non sandboxabili.

## 1. Vista d'insieme

Quattro processi, di cui tre opzionali. Il cuore e' `torchiod`; gli altri sono
satelliti che il core vede semplicemente come stampanti IPP (P1).

```
  Sessione utente
  +-------------------------------------------------------------+
  |  torchiod (spooler per-utente)                              |
  |    Queue Engine (attori per-printer + FSM + SQLite)         |
  |    Transform Engine (wazero + moduli WASM)                  |
  |    Transport (client IPP/IPPS)                              |
  |    Discovery (mDNS, sola lettura)                           |
  +-------------------------------------------------------------+
        |              |                |               |
   torchio-usb    torchio-host    stampante IPP     torchio-share
   (IPP-over-USB) (driver legacy   di rete           (server IPPS,
                   come WASM)      (Everywhere)       opzionale)
```

Superfici di controllo:

- CLI e shim `lp`/`lpr`/`lpstat` parlano col demone su Unix domain socket
  (oggi JSON-RPC; lo schema gRPC/protobuf resta il contratto previsto a termine).
- Le app GTK stampano via il portale XDG `org.freedesktop.portal.Print`.
- Ogni decisione di trust passa dal portale ush (vedi `ECOSYSTEM.md`).

## 2. Il core: torchiod

### 2.1 Modello dati

Quattro entita', serializzate in SQLite (WAL, transazioni):

- **Printer**: una destinazione, SEMPRE un endpoint IPP. Ha `URI`
  (`ipps://...` o `ipp://localhost:PORT` per i driver host), `Capabilities`
  (fotografia tipata degli attributi IPP, non un PPD), e `Origin`
  (`manual` oppure `discovered+confirmed`).
- **Capabilities**: `DocumentFormats`, `Media`, `Duplex`, `Resolutions`,
  `ColorModes`, piu' il messaggio IPP grezzo per fedelta' totale.
- **Job**: FSM piu' riferimenti ai documenti. Gli stati mappano 1:1 su
  `job-state` IPP, cosi' la condivisione e' una proiezione e non una traduzione.
  Ha un `Ticket` (copie, range pagine, media, duplex, vicino al job-template IPP)
  e un log eventi append-only.
- **DocRef**: i documenti sono blob content-addressed (SHA256) nello spool.

Decisioni dentro il modello:

- **Event sourcing leggero sui job.** Ogni transizione di stato e' una riga
  append-only con timestamp, attore e causa. `torchio queue history <job>` dice
  PERCHE' un job e' morto, non solo che e' morto.
- **Spool content-addressed.** Documenti in
  `$XDG_STATE_HOME/torchio/spool/sha256/<hash>`, fsync rigoroso, refcount nel DB.
  Ristampa dello stesso documento e' zero copie; GC e quota banali.
- **Sniffing del formato obbligatorio.** Il `Content-Type` del client e' solo un
  suggerimento; il formato vero lo decide un detector (magic bytes piu' parsing
  leggero). Un "PDF" senza `%PDF-` viene rifiutato in `validating`, prima di
  toccare qualunque trasformatore.

### 2.2 Queue engine

Una goroutine supervisore per stampante (pattern attore) che possiede la coda di
quella stampante. Niente lock condivisi: il parallelismo e' per-printer, la
serialita' e' dentro la singola printer (i job escono in ordine). Comunicazione
a canali.

La FSM dei job e' esplicita. Stati: `created -> validating -> pending ->
transforming -> spooled -> sending -> completed`, piu' `canceled`, `aborted`,
`held`. Recovery al riavvio: tutti i job in `sending` tornano `pending` (l'invio
IPP e' idempotente lato nostro: con Get-Job-Attributes scopriamo se il job era
gia' arrivato prima di re-inviare).

### 2.3 Per-utente, condivisione separata

Lo spooler e' per-utente: il confine di sicurezza del job coincide col confine
dell'utente. I documenti di un account non passano mai per un processo che possa
leggere quelli di un altro. La condivisione in rete, unico caso d'uso
genuinamente di sistema, e' un componente opzionale (`torchio-share`).

## 3. Transform engine: la pipeline come grafo, i filtri come WASM

### 3.1 Il grafo dei formati

Problema: documento in formato A, stampante che accetta {X, Y, Z}, trova la
sequenza di conversioni migliore. Torchio lo modella come un grafo i cui archi
sono i `Transform` registrati, ognuno con un costo. Il planner fa Dijkstra e
produce un piano (lista di hop).

Il costo pesa anche la FEDELTA': PDF->PDF nativo costa 0; PDF->raster costa di
piu' perche' perde la struttura vettoriale. Se la stampante accetta PDF
nativamente (caso dominante con IPP Everywhere) il piano e' vuoto e il documento
passa verbatim. Il fast path non rasterizza mai.

Formati pivot interni: `application/pdf` (lingua franca dei documenti) e
`image/pwg-raster` (lingua franca dei raster). URF/AirPrint e PCLm sono
ri-impacchettamenti di raster: archi a costo basso da PWG-raster.

### 3.2 I trasformatori sono WASM dentro wazero

Ogni Transform non-banale e' un modulo `.wasm` con interfaccia WASI minimale:
stdin il documento in ingresso, stdout quello in uscita, un blob JSON di
parametri (ticket piu' capabilities) su un fd dedicato, e nient'altro: niente
filesystem, niente rete, niente clock ad alta risoluzione.

Vantaggi rispetto ai filtri CUPS:

- **Sandbox per costruzione.** Il rasterizzatore PDF, storicamente la superficie
  d'attacco piu' grande della stampa, gira senza poter fare niente se non leggere
  e scrivere byte. Una RCE in pdfium dentro Torchio ottiene il controllo di una
  memoria lineare WASM senza syscall.
- **Portabilita' totale.** Lo stesso modulo gira su ogni arch (x86_64, aarch64,
  riscv64). I driver diventano dati, non pacchetti per-architettura.
- **Versioning e atomicita'.** Un trasformatore e' un file; aggiornarlo e'
  sostituire un file; due versioni possono coesistere. Il manifest dichiara
  `from`, `to`, `cost`, versione e hash.

### 3.3 Il problema duro: rasterizzare PDF

Non esiste un rasterizzatore PDF puro Go maturo, e scriverne uno e' un progetto
pluriennale. Strada raccomandata: **pdfium compilato a WASM, eseguito in wazero**
(go-pdfium di klippa lo fa gia' in produzione). Si ottiene il rasterizzatore di
Chromium dentro la sandbox, senza CGo. Costo: il WASM JIT e' circa 2-5x piu'
lento del nativo. Mitigazioni: rendering per bande, parallelismo per pagina (una
istanza del modulo per pagina su N worker), e il fatto che a 600dpi il collo di
bottiglia e' comunque la stampante.

Ghostscript-WASM come secondo modulo per il solo ingresso PostScript legacy.
Color management: littleCMS anch'esso a WASM; primo rilascio sRGB ovunque, il
resto e' un modulo successivo.

**Stato: implementato.** `internal/transform/pdfium.go` usa go-pdfium di klippa
(backend WebAssembly su wazero, blob `pdfium.wasm` embeddato, sandbox senza
filesystem, init pigro) e `internal/transform/pwgraster.go` e' l'encoder
PWG-Raster scritto in casa. Validato su una Brother reale: un PDF rasterizzato e
stampato fisicamente. Ghostscript-WASM e littleCMS restano da fare.

## 4. Driver legacy: torchio-host

Per il 90% delle stampanti post-2010 non servono driver (IPP Everywhere o
AirPrint). Per il resto (GDI economiche, laser che parlano solo PCL5/ESC-P),
`torchio-host` ospita driver e li espone come stampanti IPP su `localhost`
(stessa idea di PAPPL). Ma il driver e' un modulo WASM con un manifest JSON di
capability (il sostituto del PPD: tipato, validato a schema, diffabile):

```json
{
  "name": "generic-pcl5",
  "version": "1.2.0",
  "accepts": ["image/pwg-raster"],
  "produces": "vendor/pcl5",
  "capabilities": {
    "media": ["iso_a4_210x297mm", "na_letter_8.5x11in"],
    "resolutions": ["300dpi", "600dpi"],
    "duplex": ["none"],
    "color": ["monochrome"]
  },
  "quirks": ["needs-trailing-formfeed"]
}
```

Un database di **quirks** (stringhe `key=value` per modello, fuori dal codice)
raccoglie le perversioni note dei firmware.

## 5. Trasporti, USB e discovery

- **Trasporto.** Client IPP `ipp://` e `ipps://` (TLS con TOFU: alla prima
  connessione il certificato e' mostrato via portale e pinnato nel DB). Il socket
  raw 9100/JetDirect esiste solo come trasporto interno di `torchio-host`. Mattoni
  Go maturi: `goipp` (di OpenPrinting) e `ipp-usb` (gia' in Go).
- **USB.** `torchio-usb` fa IPP-over-USB (classe 7.1.4) parlando con usbfs
  (`/dev/bus/usb/*`) via ioctl, niente libusb. Unico componente che tocca
  hardware, confinato con landlock alla sola `/dev/bus/usb`; il portale conferma
  il primo uso di ogni nuovo device.
- **Discovery, a sola lettura per costituzione.** Il modulo mDNS/DNS-SD mantiene
  solo una tabella effimera `devices_seen`. La discovery non crea MAI nulla:
  toccare una stampante trovata scatena la richiesta via portale, e solo la
  conferma crea il record `Printer` con `Origin: discovered+confirmed`. Questa
  singola scelta elimina l'intera classe di vulnerabilita' del 2024.

## 6. Condivisione: torchio-share (opzionale)

Server IPPS separato, assente di default. Ascolta IPPS-only, autentica via mTLS o
credenziali, si comporta da client verso lo spooler dell'utente che condivide
(proiezione, non duplicazione). Annuncia via DNS-SD come stampante IPP
Everywhere, cosi' qualunque OS la vede senza driver. Unico componente con una
porta aperta.

## 7. Modello di sicurezza

| Superficie | Mitigazione |
|---|---|
| Documento ostile (PDF malformato) | parsing solo dentro WASM senza syscall; sniffing prima della pipeline |
| Rete locale ostile | discovery read-only; conferma via portale; IPPS piu' TOFU; nessuna porta aperta di default |
| Driver vendor ostile o buggato | driver come WASM sandboxato con manifest dichiarativo; nessun codice nativo di terze parti |
| Escalation locale | demone per-utente senza privilegi; USB confinato con landlock piu' udev; share separato |
| Stato corrotto | SQLite WAL piu' transazioni; spool content-addressed verificabile (`torchio doctor --fsck`) |
| Supply chain | un binario Go riproducibile piu' moduli WASM con hash nel manifest, firmabili |

Essendo Go: niente intere classi di memory bug nel demone stesso; il C resta
solo dentro le sandbox WASM.

## 8. Rischi noti

1. **Qualita' di resa.** La rasterizzazione e' dove i sistemi di stampa muoiono
   di mille tagli (font sostituiti, mezzitoni brutti, margini sballati). Pdfium
   da' una base eccellente; il fast path PDF evita di rasterizzare sulla
   maggioranza delle stampanti moderne.
2. **Performance WASM.** 2-5x sul nativo, quasi sempre irrilevante (la stampante
   e' piu' lenta), ma un poster A0 a 1200dpi lo fa sentire. L'interfaccia
   `Transform` resta pronta a un backend nativo opzionale.
3. **La palude dei quirks.** Vent'anni di firmware rotti non si aggirano con
   l'eleganza. Il quirks DB testuale e' la valvola di sfogo, tenuto fuori dal
   codice.
4. **Scope creep.** L'ordine della roadmap rende ogni milestone usabile da sola.
