package publisher

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateBrokerURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "mqtts", url: "mqtts://broker.example.net:8883"},
		{name: "ssl", url: "ssl://broker.example.net:8883"},
		{name: "tls", url: "tls://broker.example.net:8883"},
		{name: "tcp", url: "tcp://localhost:1883"},
		{name: "websockets over tls", url: "wss://broker.example.net:443"},
		{name: "a unix socket needs no host", url: "unix:///run/mosquitto.sock"},
		{
			// The likeliest paste: a host and port with no scheme, which
			// the client would refuse far away from here.
			name:    "no scheme names the fix",
			url:     "broker.example.net:8883",
			wantErr: "no scheme",
		},
		{name: "an unsupported scheme is named", url: "https://broker.example.net", wantErr: "unsupported scheme"},
		{name: "a scheme with no host", url: "mqtts://", wantErr: "no host"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBrokerURL(tt.url)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("ValidateBrokerURL(%q) = %v, want nil", tt.url, err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("ValidateBrokerURL(%q) = nil, want an error mentioning %q", tt.url, tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("ValidateBrokerURL(%q) = %v, want it to mention %q", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestSchemeIsTLS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url  string
		want bool
	}{
		{url: "mqtts://b:8883", want: true},
		{url: "ssl://b:8883", want: true},
		{url: "tls://b:8883", want: true},
		{url: "wss://b:443", want: true},
		{url: "MQTTS://b:8883", want: true}, // schemes are case-insensitive
		{url: "tcp://b:1883"},
		{url: "ws://b:80"},
		{url: "mqtt://b:1883"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := SchemeIsTLS(tt.url); got != tt.want {
				t.Errorf("SchemeIsTLS(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestBuildTLSConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	writeTestCA(t, caPath)
	notPEM := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notPEM, []byte("this is not a certificate\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("a plaintext url gets no tls config", func(t *testing.T) {
		cfg, err := buildTLSConfig("tcp://localhost:1883", TLSOptions{})
		if err != nil || cfg != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", cfg, err)
		}
	})

	t.Run("tls options against a plaintext url are an error", func(t *testing.T) {
		// Silently ignoring them would leave the operator believing the
		// connection is protected when it is not.
		_, err := buildTLSConfig("tcp://localhost:1883", TLSOptions{CAFile: caPath})
		if err == nil {
			t.Error("buildTLSConfig accepted TLS options on a plaintext url")
		}
	})

	t.Run("mqtts with no options uses system trust", func(t *testing.T) {
		cfg, err := buildTLSConfig("mqtts://broker.example.net:8883", TLSOptions{})
		if err != nil {
			t.Fatalf("buildTLSConfig: %v", err)
		}
		if cfg == nil {
			t.Fatal("got no config for an mqtts url")
		}
		if cfg.RootCAs != nil {
			t.Error("RootCAs set without a ca file; want the system pool")
		}
		if cfg.MinVersion != 0x0303 {
			t.Errorf("MinVersion = %#x, want TLS 1.2", cfg.MinVersion)
		}
	})

	t.Run("a ca file replaces the pool", func(t *testing.T) {
		cfg, err := buildTLSConfig("mqtts://broker.example.net:8883", TLSOptions{CAFile: caPath})
		if err != nil {
			t.Fatalf("buildTLSConfig: %v", err)
		}
		if cfg.RootCAs == nil {
			t.Error("RootCAs not set from the ca file")
		}
	})

	t.Run("a file with no certificates is named as such", func(t *testing.T) {
		// AppendCertsFromPEM reports only that nothing was added, which
		// for a file the operator believes is a CA bundle is worth
		// saying plainly rather than failing later at connect time.
		_, err := buildTLSConfig("mqtts://b:8883", TLSOptions{CAFile: notPEM})
		if err == nil || !strings.Contains(err.Error(), "no PEM certificates") {
			t.Errorf("err = %v, want it to name the empty bundle", err)
		}
	})

	t.Run("a missing ca file is an error", func(t *testing.T) {
		if _, err := buildTLSConfig("mqtts://b:8883", TLSOptions{CAFile: filepath.Join(dir, "absent.pem")}); err == nil {
			t.Error("buildTLSConfig accepted a ca file that does not exist")
		}
	})

	t.Run("half a client keypair is an error", func(t *testing.T) {
		for _, opts := range []TLSOptions{{CertFile: "c.pem"}, {KeyFile: "k.pem"}} {
			if _, err := buildTLSConfig("mqtts://b:8883", opts); err == nil {
				t.Errorf("buildTLSConfig(%+v) accepted half a keypair", opts)
			}
		}
	})

	t.Run("insecure is carried through", func(t *testing.T) {
		cfg, err := buildTLSConfig("mqtts://b:8883", TLSOptions{Insecure: true})
		if err != nil {
			t.Fatalf("buildTLSConfig: %v", err)
		}
		if !cfg.InsecureSkipVerify {
			t.Error("InsecureSkipVerify not set")
		}
	})
}

// writeTestCA generates a throwaway self-signed CA, so the parsing path
// is exercised against a real certificate rather than a fixture that
// could drift out of validity.
func writeTestCA(t *testing.T, path string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "sparkwrangler test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
}
