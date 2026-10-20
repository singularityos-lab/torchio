# Packaging torchio

torchiod runs once per user session. It serves the CUPS protocol on the socket
named by `CUPS_SERVER`, so libcups clients and the CUPS command line tools
(`lp`, `lpstat`, `lpadmin`, `cancel`, `cupsenable`) talk to it unchanged. Ship
libcups and the CUPS clients, not cupsd.

## Files

- Binaries: `/usr/bin/torchio` and `/usr/bin/torchiod`.

      make static

- Session start: `torchiod.desktop` to `/etc/xdg/autostart/`, or
  `torchiod.service` to `/usr/lib/systemd/user/` on systemd sessions.
- Session environment: export
  `CUPS_SERVER=$XDG_RUNTIME_DIR/torchio/cups.sock` before applications start.
- Trust: the ush broker is the default. Elsewhere set `TORCHIO_TRUST_BACKEND`
  to `polkit` and install `org.torchio.trust.policy` to
  `/usr/share/polkit-1/actions/`, or to `policy` with a rule file such as
  `trust.policy`.
