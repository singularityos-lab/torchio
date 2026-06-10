package ipp

import (
	"fmt"
	"net/url"
	"strings"
)

// DefaultPort is the IANA port for IPP. Both ipp and ipps default to it.
const DefaultPort = "631"

// normalizeURI takes a user-supplied printer URI and returns two forms:
//
//   - canonical: the ipp:// or ipps:// URI to put in the IPP "printer-uri"
//     operation attribute (printers match jobs against this exact value).
//   - transport: the http:// or https:// URL that net/http actually dials,
//     since Go's HTTP client does not understand the ipp scheme.
//
// ipp maps to http, ipps maps to https, and a missing port defaults to 631.
// http/https inputs are accepted too (the canonical form is mapped back to
// ipp/ipps) so the CLI is forgiving about which scheme the user types.
func normalizeURI(raw string) (canonical, transport string, err error) {
	if raw == "" {
		return "", "", fmt.Errorf("empty printer URI")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid printer URI %q: %w", raw, err)
	}

	var canonScheme, transScheme string
	switch strings.ToLower(u.Scheme) {
	case "ipp", "http":
		canonScheme, transScheme = "ipp", "http"
	case "ipps", "https":
		canonScheme, transScheme = "ipps", "https"
	case "":
		return "", "", fmt.Errorf("printer URI %q has no scheme (try ipp:// or ipps://)", raw)
	default:
		return "", "", fmt.Errorf("unsupported printer URI scheme %q", u.Scheme)
	}

	host := u.Host
	if host == "" {
		return "", "", fmt.Errorf("printer URI %q has no host", raw)
	}
	if u.Port() == "" {
		host = host + ":" + DefaultPort
	}

	path := u.Path
	if path == "" {
		// Most IPP Everywhere printers live at /ipp/print; do not invent a
		// path silently, but a bare host is almost always this.
		path = "/ipp/print"
	}

	canon := url.URL{Scheme: canonScheme, Host: host, Path: path}
	trans := url.URL{Scheme: transScheme, Host: host, Path: path}
	return canon.String(), trans.String(), nil
}
