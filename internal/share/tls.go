// Package share implements torchio-share: it re-exposes a local print queue to
// the network over IPPS (encrypted IPP), announces it via DNS-SD so any other
// OS sees it as a driverless IPP Everywhere printer, and forwards incoming jobs
// to the user's spooler (a projection of the queue, not a duplicate). It is the
// only component with an open network port and is absent from the image by
// default.
package share

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"time"
)

// SelfSignedCert generates an ECDSA P-256 self-signed certificate for the given
// host names (plus loopback). IPPS trust in Torchio is established out of band
// (TOFU through the portal), not via the public CA system, so a self-signed
// cert is the right default; the fingerprint is what gets pinned.
func SelfSignedCert(hostnames ...string) (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return tls.Certificate{}, err
	}

	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "torchio-share"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string{"localhost"}, hostnames...),
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv, Leaf: &tmpl}, nil
}
