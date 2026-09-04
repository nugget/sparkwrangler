// Package config resolves settings from flags and the environment.
//
// Flags and environment rather than a config file, and no YAML
// dependency: every setting is a scalar, a systemd unit or a container
// spec documents itself when the settings are visible in it, and the one
// secret is a broker password that belongs in an environment file with
// its own permissions rather than in a config committed by accident.
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is one node's settings.
type Config struct {
	NodeID   string
	NodeName string

	// VLLMURL is empty on a node that runs no engine — a tensor-parallel
	// worker, where only the head node serves the API. See
	// [Config.WorkerMode].
	VLLMURL     string
	VLLMTimeout time.Duration
	// ConfigURL is the address Home Assistant links to on the device
	// page. It is not derived from VLLMURL, which is normally loopback
	// and would send a browser to its own machine.
	ConfigURL string

	BrokerURL       string
	ClientID        string
	Username        string
	Password        string
	DiscoveryPrefix string
	TopicPrefix     string
	QoS             int

	CAFile      string
	CertFile    string
	KeyFile     string
	TLSServer   string
	TLSInsecure bool

	Interval time.Duration

	NvidiaSMIPath string
	DeviceModel   string
	// NetInterface names the port whose MAC identifies this node, when
	// the automatic choice is wrong. Empty selects the interface carrying
	// the default route.
	NetInterface string
	// Area is the Home Assistant area to suggest for this node's device.
	// It is the only remaining way to put this device beside the other
	// records for the same machine, the registry having stopped merging
	// them in 2026.8.
	Area     string
	LogLevel string
}

// Load parses flags, falling back to environment variables and then to
// defaults. Every flag has a SPARKWRANGLER_ prefixed environment
// equivalent, which is what a container or a systemd unit will use.
func Load(args []string) (Config, error) {
	fs := flag.NewFlagSet("sparkwrangler", flag.ContinueOnError)

	hostname, _ := os.Hostname()
	// Only the short name: a node identified as spark-01.example.net
	// produces entity ids carrying dots, which are legal and unpleasant.
	if i := strings.Index(hostname, "."); i > 0 {
		hostname = hostname[:i]
	}

	var c Config
	fs.StringVar(&c.NodeID, "node-id", env("NODE_ID", hostname), "stable identifier for this node; entity ids derive from it")
	fs.StringVar(&c.NodeName, "node-name", env("NODE_NAME", ""), "display name in Home Assistant (default: node id)")
	fs.StringVar(&c.VLLMURL, "vllm-url", env("VLLM_URL", "http://localhost:8000"), "base URL of the vLLM server on this node; empty for a node that runs no engine")
	fs.StringVar(&c.ConfigURL, "config-url", env("CONFIG_URL", ""), "address Home Assistant links to on the device page; unset publishes no link")
	fs.DurationVar(&c.VLLMTimeout, "vllm-timeout", envDuration("VLLM_TIMEOUT", 5*time.Second), "per-request timeout for vLLM reads")
	fs.StringVar(&c.BrokerURL, "broker", env("BROKER", "tcp://localhost:1883"), "MQTT broker URL: mqtts:// for TLS, tcp:// for plaintext")
	fs.StringVar(&c.ClientID, "client-id", env("CLIENT_ID", ""), "MQTT client id (default: sparkwrangler-<node id>)")
	fs.StringVar(&c.Username, "username", env("USERNAME", ""), "MQTT username")
	fs.StringVar(&c.Password, "password", env("PASSWORD", ""), "MQTT password; prefer the environment over the command line")
	fs.StringVar(&c.DiscoveryPrefix, "discovery-prefix", env("DISCOVERY_PREFIX", "homeassistant"), "Home Assistant discovery topic prefix")
	fs.StringVar(&c.TopicPrefix, "topic-prefix", env("TOPIC_PREFIX", "sparkwrangler"), "topic prefix for state and availability")
	fs.IntVar(&c.QoS, "qos", envInt("QOS", 1), "MQTT QoS for published messages")
	fs.StringVar(&c.CAFile, "ca-file", env("CA_FILE", ""), "PEM bundle to verify the broker against; needed for a private or self-signed CA")
	fs.StringVar(&c.CertFile, "tls-cert", env("TLS_CERT", ""), "client certificate for mutual TLS")
	fs.StringVar(&c.KeyFile, "tls-key", env("TLS_KEY", ""), "client key for mutual TLS")
	fs.StringVar(&c.TLSServer, "tls-servername", env("TLS_SERVERNAME", ""), "name to verify against the broker certificate, when it differs from the URL host")
	fs.BoolVar(&c.TLSInsecure, "tls-insecure", envBool("TLS_INSECURE", false), "skip broker certificate verification; for debugging a first connection only")
	fs.DurationVar(&c.Interval, "interval", envDuration("INTERVAL", 15*time.Second), "how often to publish state")
	fs.StringVar(&c.NvidiaSMIPath, "nvidia-smi", env("NVIDIA_SMI", ""), "path to nvidia-smi (default: resolve on PATH)")
	fs.StringVar(&c.DeviceModel, "device-model", env("DEVICE_MODEL", ""), "hardware model shown in Home Assistant")
	fs.StringVar(&c.NetInterface, "net-interface", env("NET_INTERFACE", ""), "interface whose MAC identifies this node (default: the one carrying the default route)")
	fs.StringVar(&c.Area, "area", env("AREA", ""), "Home Assistant area to suggest for this node, so it lands beside the other devices for the same machine")
	fs.StringVar(&c.LogLevel, "log-level", env("LOG_LEVEL", "info"), "debug, info, warn or error")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return c, c.validate()
}

// WorkerMode reports whether this node runs no engine of its own. On a
// tensor-parallel cluster only the head node serves the API; the others
// hold half the weights and have an accelerator worth watching, but
// nothing to ask about serving.
func (c Config) WorkerMode() bool {
	return strings.TrimSpace(c.VLLMURL) == ""
}

func (c Config) validate() error {
	if c.NodeID == "" {
		return fmt.Errorf("node id is empty and the hostname could not supply one; set -node-id")
	}
	if c.Interval < time.Second {
		// Every interval is an HTTP scrape, an exec of nvidia-smi and a
		// retained publish. Sub-second polling costs more than it tells
		// anyone.
		return fmt.Errorf("interval %s is too short; use at least 1s", c.Interval)
	}
	if c.QoS < 0 || c.QoS > 2 {
		return fmt.Errorf("qos %d is not 0, 1 or 2", c.QoS)
	}
	return nil
}

func env(name, fallback string) string {
	if v, ok := os.LookupEnv("SPARKWRANGLER_" + name); ok {
		return v
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	if v, err := strconv.ParseBool(env(name, "")); err == nil {
		return v
	}
	return fallback
}

func envInt(name string, fallback int) int {
	if v, err := strconv.Atoi(env(name, "")); err == nil {
		return v
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(env(name, "")); err == nil {
		return d
	}
	return fallback
}
