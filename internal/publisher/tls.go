package publisher

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// TLSOptions configures the transport security of the broker
// connection.
//
// The zero value means system trust, which is right for a broker holding
// a certificate from a public CA and wrong for most home installations —
// a Mosquitto add-on is usually presenting either a self-signed
// certificate or one from a private CA, and both need CAFile.
type TLSOptions struct {
	// CAFile is a PEM bundle to verify the broker against, replacing the
	// system pool rather than adding to it. A private CA is the common
	// case for a broker on a home network.
	CAFile string

	// CertFile and KeyFile enable mutual TLS, where the broker
	// authenticates the client by certificate instead of, or in addition
	// to, a password.
	CertFile string
	KeyFile  string

	// ServerName overrides the name verified against the certificate,
	// for a broker reached by an address its certificate does not name.
	ServerName string

	// Insecure disables verification entirely. It exists because a
	// first connection to an unfamiliar broker is easier to debug with
	// it than without, and it is logged loudly every time, because a
	// connection that does not verify the broker cannot tell it apart
	// from anything else answering on that port.
	Insecure bool
}

// SchemeIsTLS reports whether a broker URL asks for transport security.
// The three spellings are all accepted by the MQTT client; mqtts is the
// one worth writing.
func SchemeIsTLS(brokerURL string) bool {
	u, err := url.Parse(brokerURL)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "mqtts", "ssl", "tls", "wss":
		return true
	default:
		return false
	}
}

// ValidateBrokerURL rejects a broker URL the MQTT client would refuse,
// so the failure names the problem rather than surfacing as a connection
// error at the far end of a retry loop.
func ValidateBrokerURL(brokerURL string) error {
	// Checked before parsing, because a bare host:port parses cleanly
	// with the hostname as the scheme — so the likeliest mistake would
	// otherwise be reported as an unsupported scheme named after the
	// broker, which reads as nonsense.
	if !strings.Contains(brokerURL, "://") {
		return fmt.Errorf("broker url %q has no scheme; use mqtts:// for TLS or tcp:// for plaintext", brokerURL)
	}
	u, err := url.Parse(brokerURL)
	if err != nil {
		return fmt.Errorf("broker url %q: %w", brokerURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "mqtts", "ssl", "tls", "wss", "mqtt", "tcp", "ws", "unix":
		// Recognised by the client library.
	default:
		return fmt.Errorf("broker url %q has unsupported scheme %q; use mqtts, tcp, ssl, tls, ws, wss or unix", brokerURL, u.Scheme)
	}
	if u.Scheme != "unix" && u.Host == "" {
		return fmt.Errorf("broker url %q has no host", brokerURL)
	}
	return nil
}

// buildTLSConfig returns the configuration for a TLS broker connection,
// or nil when the URL asks for plaintext. A nil result is what the MQTT
// client wants in that case.
func buildTLSConfig(brokerURL string, opts TLSOptions) (*tls.Config, error) {
	if !SchemeIsTLS(brokerURL) {
		// Options set against a plaintext URL are a mistake worth
		// naming rather than ignoring: the operator believes the
		// connection is protected and it is not.
		//
		// Compared against the zero value rather than field by field.
		// An enumeration here was wrong within a day of being written —
		// it listed CAFile, CertFile and Insecure, so KeyFile and
		// ServerName were silently accepted and dropped — and it would
		// go wrong again the next time a field is added. TLSOptions is
		// all comparable fields, so this stays correct on its own.
		if opts != (TLSOptions{}) {
			return nil, fmt.Errorf("tls options set but broker url %q is not a TLS scheme; use mqtts://", brokerURL)
		}
		return nil, nil
	}

	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: opts.Insecure,
		ServerName:         opts.ServerName,
	}

	if opts.CAFile != "" {
		pem, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			// AppendCertsFromPEM reports only that nothing was added,
			// which for a file the operator believes is a CA bundle is
			// worth saying plainly.
			return nil, fmt.Errorf("ca file %q contains no PEM certificates", opts.CAFile)
		}
		cfg.RootCAs = pool
	}

	switch {
	case opts.CertFile != "" && opts.KeyFile != "":
		pair, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	case opts.CertFile != "" || opts.KeyFile != "":
		return nil, fmt.Errorf("mutual TLS needs both a certificate and a key; only one was given")
	}

	return cfg, nil
}
