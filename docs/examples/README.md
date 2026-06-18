# Trust backend examples

Torchio delegates trust decisions (add a printer, trust a certificate, claim a
USB device, share a queue) through the `Portal` interface in `internal/portal`.
The ush broker is the default and the only backend used on Singularity
(principle P4). Other distributions select a different backend at startup:

```sh
TORCHIO_TRUST_BACKEND=ush     torchio daemon   # default; delegates to ush
TORCHIO_TRUST_BACKEND=polkit  torchio daemon   # PolicyKit
TORCHIO_TRUST_BACKEND=policy  torchio daemon   # declarative rule file
TORCHIO_TRUST_BACKEND=deny    torchio daemon   # refuse everything (safe)
```

Any failure to build the chosen backend falls back to deny, so trust never
silently opens. Direct printing and the CUPS-compatible front end work without
any portal; a backend is only needed for the trust gestures above.

## Files

- `trust.policy` - a sample rule file for `TORCHIO_TRUST_BACKEND=policy`.
  Default location `$XDG_CONFIG_HOME/torchio/trust.policy`, or set
  `TORCHIO_TRUST_POLICY`. First match wins; missing file means deny-by-default.

- `org.torchio.trust.policy` - PolicyKit actions for
  `TORCHIO_TRUST_BACKEND=polkit`. Install to
  `/usr/share/polkit-1/actions/org.torchio.trust.policy`. Each permission
  category maps to one `org.torchio.trust.<category>` action.
