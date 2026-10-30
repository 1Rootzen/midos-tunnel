package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

// tlsHandshakeTimeout bounds the TLS handshake on a fresh carrier. A variable
// so tests can shorten it.
var tlsHandshakeTimeout = 15 * time.Second

// fingerprints maps a friendly name to a browser ClientHello.
//
// This is the whole point of the component. Go's own TLS stack emits a
// ClientHello with a distinctive extension order, cipher list and curve set;
// once someone writes a rule for it, every connection the program makes is
// recognisable no matter what port or address it uses. Replaying a real
// browser's hello removes that handle.
func helloID(name string) utls.ClientHelloID {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "firefox":
		return utls.HelloFirefox_Auto
	case "safari":
		return utls.HelloSafari_Auto
	case "edge":
		return utls.HelloEdge_Auto
	case "random":
		return utls.HelloRandomizedALPN
	default:
		return utls.HelloChrome_Auto
	}
}

// dialTLS opens a tls or ws carrier: TCP, then a browser ClientHello, then for
// ws the WebSocket upgrade. It also returns the TCP connection underneath, so
// the caller can read the kernel's view of the path.
func dialTLS(cfg *Config, ep Endpoint) (net.Conn, net.Conn, error) {
	raw, err := net.DialTimeout("tcp", ep.Server, dialTimeout)
	if err != nil {
		return nil, nil, err
	}
	tuneTCP(raw)

	var hc net.Conn = raw
	if cfg.TLSFragment {
		hc = &fragConn{Conn: raw}
	}

	tc := &utls.Config{
		ServerName: cfg.ServerName,
		// The origin is often a self-signed or CDN-terminated certificate, so
		// certificate identity is not what authenticates the peer here - the
		// pre-shared key challenge inside the session is. Verification is still
		// on by default for anyone who does have a real certificate.
		InsecureSkipVerify: cfg.Insecure,
	}
	var c *utls.UConn
	if ep.Transport == "ws" {
		// A WebSocket upgrade is HTTP/1.1; a browser hello offering h2 would
		// have the CDN switch to HTTP/2 and refuse it. Same browser hello,
		// with ALPN narrowed to http/1.1 as a browser does for a WebSocket.
		c, err = wsHelloConn(hc, tc, cfg.Fingerprint)
		if err != nil {
			_ = raw.Close()
			return nil, nil, err
		}
	} else {
		c = utls.UClient(hc, tc, helloID(cfg.Fingerprint))
	}

	// Some networks let the TCP handshake through and then silently drop the
	// ClientHello. Without a deadline the handshake would wait forever, and
	// every user connection would strand one more socket behind it.
	_ = raw.SetDeadline(time.Now().Add(tlsHandshakeTimeout))
	if err := c.Handshake(); err != nil {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("tls handshake with %s: %w", ep.Server, err)
	}
	_ = raw.SetDeadline(time.Time{})
	if ep.Transport != "ws" {
		return c, raw, nil
	}
	ws, err := wsClientUpgrade(c, cfg.Host, cfg.WSPath)
	if err != nil {
		_ = c.Close()
		return nil, nil, fmt.Errorf("%s: %w", ep.Server, err)
	}
	return ws, raw, nil
}

// wsHelloConn builds a browser ClientHello whose ALPN offers only http/1.1.
func wsHelloConn(raw net.Conn, tc *utls.Config, fingerprint string) (*utls.UConn, error) {
	id := helloID(fingerprint)
	if id == utls.HelloRandomizedALPN {
		id = utls.HelloChrome_Auto // a randomized hello has no fixed spec to edit
	}
	spec, err := utls.UTLSIdToSpec(id)
	if err != nil {
		return nil, fmt.Errorf("browser hello: %w", err)
	}
	for _, ext := range spec.Extensions {
		if a, ok := ext.(*utls.ALPNExtension); ok {
			a.AlpnProtocols = []string{"http/1.1"}
		}
	}
	c := utls.UClient(raw, tc, utls.HelloCustom)
	if err := c.ApplyPreset(&spec); err != nil {
		return nil, fmt.Errorf("browser hello: %w", err)
	}
	return c, nil
}

// serverTLSConfig loads the configured certificate, or makes a throwaway one.
func serverTLSConfig(cfg *Config) (*tls.Config, error) {
	var cert tls.Certificate
	var err error
	if cfg.CertPEM != "" && cfg.KeyPEM != "" {
		cert, err = tls.LoadX509KeyPair(cfg.CertPEM, cfg.KeyPEM)
		if err != nil {
			return nil, fmt.Errorf("certificate: %w", err)
		}
	} else {
		cert, err = selfSigned(cfg.ServerName)
		if err != nil {
			return nil, err
		}
	}
	protos := []string{"h2", "http/1.1"}
	if cfg.WSPath != "" {
		// A CDN forwarding a WebSocket must stay on HTTP/1.1, the only version
		// the upgrade exists in; a site without h2 is entirely ordinary.
		protos = []string{"http/1.1"}
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   protos,
	}, nil
}

// selfSigned builds an in-memory certificate so the server can run before the
// operator has a real one. The pre-shared key is what proves identity, so this
// is not a security shortcut - but a real certificate still looks more ordinary
// to anyone inspecting the handshake.
func selfSigned(name string) (tls.Certificate, error) {
	if name == "" {
		name = "localhost"
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		DNSNames:              []string{name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(2, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
}
