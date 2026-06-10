# Torchio nell'ecosistema Singularity

Torchio non e' un'isola: e' il sistema di stampa della distro Singularity di
Mirko. Tre integrazioni concrete.

## 1. ush, il portale dei permessi (P4)

[ush](https://github.com/mirkobrombin/ush) e' il broker di sicurezza piu' portale
dell'ecosistema: il chokepoint di TUTTE le decisioni di trust, al posto di
PolicyKit/sudo. Torchio non implementa una propria UI di consenso, delega a ush.

Contratto D-Bus (da `ush/docs/PORTAL_SPEC.md`):

- **Portal** (lo possiede il desktop, mostra il dialogo nativo):
  - bus `io.github.mirkobrombin.ush.Portal`
  - path `/io/github/mirkobrombin/ush/Portal`
  - iface `io.github.mirkobrombin.ush.Portal1`
  - metodo `ShowPermission(category s, resource s, reason s) -> decision s`
- **Broker** (deriva lo scope minimo, applica le policy, fa l'audit):
  - bus `io.github.mirkobrombin.ush.Broker`, iface `...Broker1`
  - `RequestPermission(category, resource, reason, sessionID) -> decision`
  - `AllowApp`/`DenyApp`/`IsAppTrusted` per il trust per-app
- **Categorie**: `network`, `device`, `filesystem`, `dbus.sensitive`.
- **Decisioni**: `allow`, `allow_session`, `allow_always`, `deny`.
- Il broker riduce lo scope prima di mostrarlo (es. un path diventa la sua dir
  genitore), ma i path sensibili (`.env`, `.ssh`, `.gnupg`, secret/token) restano
  esatti e non sono mai allargati.

Dove Torchio chiama il portale:

| Evento Torchio | category | resource | reason (esempio) |
|---|---|---|---|
| Aggiunta di una stampante trovata in rete | `network` | `ipps://host:631` | "Aggiungere HP LaserJet trovata in rete?" |
| Primo uso di un device USB (hotplug) | `device` | `/dev/bus/usb/...` | "Usare la stampante USB appena collegata?" |
| Accettazione certificato TLS (TOFU) | `network` | `ipps://host:631` | "Fidarsi del certificato di questa stampante?" |
| Condivisione di una coda in rete | `network` | `outbound` | "Condividere la coda Ufficio in rete?" |

Inoltre Torchio implementa il backend del portale XDG standard
`org.freedesktop.portal.Print`, cosi' le app GTK sandboxate stampano senza
saperne nulla.

## 2. singularity-os, la distro buildroot (P5)

[singularity-os](../singularity-os) e' buildroot-based. I pacchetti Go si
costruiscono con il macro `host-golang-package`. Template di riferimento:
`package/vetro/vetro.mk`.

Pacchetto Torchio previsto (`package/torchio/torchio.mk`):

```make
TORCHIO_VERSION = <commit>
TORCHIO_SITE = $(call github,singularityos-lab,torchio,$(TORCHIO_VERSION))
TORCHIO_LICENSE = GPL-3.0-or-later

HOST_TORCHIO_BUILD_TARGETS = ./cmd/torchio
HOST_TORCHIO_LDFLAGS = -X main.version=$(TORCHIO_VERSION)

$(eval $(host-golang-package))
```

Layout pacchetto sul target:

```
/usr/bin/torchio            + symlink lp, lpr, lpstat
/usr/share/torchio/transforms/*.wasm   (pdfium, ecc.)
/usr/share/torchio/drivers/*.wasm      (opzionali)
/etc/torchio/config.toml
```

Vincoli che la distro impone (e che guidano il design):

- `CGO_ENABLED=0`, `-trimpath`, link statico, build riproducibile.
- Dipendenze runtime native: ZERO. Niente avahi (mDNS interno), niente libusb
  (usbfs diretto), niente ghostscript/poppler di sistema (WASM), niente dbus
  obbligatorio (gRPC su UDS; il backend portal e' l'unico punto D-Bus).
- Config nell'immagine, stato in `/var` (e in `$XDG_STATE_HOME` per-utente).

Footprint stimato: binario circa 15-25 MB, `pdfium.wasm` circa 10 MB. Profilo
"headless senza stampa raster": si tolgono i `.wasm` e resta il fast path
PDF-passthrough in pochi MB.

## 3. singularity-desktop, il consumatore degli eventi

[singularity-desktop](../singularity-desktop) e' la shell Vala/GTK4. Si abbona
allo stream gRPC server-side di Torchio e mostra il progresso dei job senza
polling. Possiede anche il bus name del portale ush per disegnare i dialoghi
nativi di consenso (vedi sopra).

## Convenzioni ereditate dall'ecosistema

- Module path `github.com/mirkobrombin/<name>`, Go 1.26, `CGO_ENABLED=0`.
- Licenza GPL-3.0-or-later, header SPDX in cima ai sorgenti.
- Un hook del repo dell'utente vieta em-dash e frecce unicode nei file: usare
  punteggiatura ASCII e `->` semplice.
