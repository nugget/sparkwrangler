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

	VLLMURL     string
	VLLMTimeout time.Duration

	BrokerURL       string
	ClientID        string
	Username        string
	Password        string
	DiscoveryPrefix string
	TopicPrefix     string
	QoS             int

	Interval time.Duration

	NvidiaSMIPath string
	DeviceModel   string
	LogLevel      string
}

// Load parses flags, falling back to environment variables and then to
// defaults. Every flag has a SPARKRUSTLER_ prefixed environment
// equivalent, which is what a container or a systemd unit will use.
func Load(args []string) (Config, error) {
	fs := flag.NewFlagSet("sparkrustler", flag.ContinueOnError)

	hostname, _ := os.Hostname()
	// Only the short name: a node identified as spark-a23e.example.net
	// produces entity ids carrying dots, which are legal and unpleasant.
	if i := strings.Index(hostname, "."); i > 0 {
		hostname = hostname[:i]
	}

	var c Config
	fs.StringVar(&c.NodeID, "node-id", env("NODE_ID", hostname), "stable identifier for this node; entity ids derive from it")
	fs.StringVar(&c.NodeName, "node-name", env("NODE_NAME", ""), "display name in Home Assistant (default: node id)")
	fs.StringVar(&c.VLLMURL, "vllm-url", env("VLLM_URL", "http://localhost:8000"), "base URL of the vLLM server on this node")
	fs.DurationVar(&c.VLLMTimeout, "vllm-timeout", envDuration("VLLM_TIMEOUT", 5*time.Second), "per-request timeout for vLLM reads")
	fs.StringVar(&c.BrokerURL, "broker", env("BROKER", "tcp://localhost:1883"), "MQTT broker URL")
	fs.StringVar(&c.ClientID, "client-id", env("CLIENT_ID", ""), "MQTT client id (default: sparkrustler-<node id>)")
	fs.StringVar(&c.Username, "username", env("USERNAME", ""), "MQTT username")
	fs.StringVar(&c.Password, "password", env("PASSWORD", ""), "MQTT password; prefer the environment over the command line")
	fs.StringVar(&c.DiscoveryPrefix, "discovery-prefix", env("DISCOVERY_PREFIX", "homeassistant"), "Home Assistant discovery topic prefix")
	fs.StringVar(&c.TopicPrefix, "topic-prefix", env("TOPIC_PREFIX", "sparkrustler"), "topic prefix for state and availability")
	fs.IntVar(&c.QoS, "qos", envInt("QOS", 1), "MQTT QoS for published messages")
	fs.DurationVar(&c.Interval, "interval", envDuration("INTERVAL", 15*time.Second), "how often to publish state")
	fs.StringVar(&c.NvidiaSMIPath, "nvidia-smi", env("NVIDIA_SMI", ""), "path to nvidia-smi (default: resolve on PATH)")
	fs.StringVar(&c.DeviceModel, "device-model", env("DEVICE_MODEL", ""), "hardware model shown in Home Assistant")
	fs.StringVar(&c.LogLevel, "log-level", env("LOG_LEVEL", "info"), "debug, info, warn or error")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return c, c.validate()
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
	if v, ok := os.LookupEnv("SPARKRUSTLER_" + name); ok {
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
