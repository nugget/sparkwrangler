package config

import (
	"testing"
	"time"
)

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "defaults are usable", args: []string{"-node-id", "spark-a23e"}},
		{
			// Each interval is an HTTP scrape, an exec of nvidia-smi and
			// a retained publish; sub-second polling costs more than it
			// tells anyone.
			name:    "a sub-second interval is refused",
			args:    []string{"-node-id", "n", "-interval", "200ms"},
			wantErr: true,
		},
		{name: "an out-of-range qos is refused", args: []string{"-node-id", "n", "-qos", "3"}, wantErr: true},
		{name: "an empty node id is refused", args: []string{"-node-id", ""}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.args)
			if tt.wantErr && err == nil {
				t.Error("Load succeeded, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Load: %v", err)
			}
		})
	}
}

// TestFlagsBeatEnvironment pins the precedence a systemd unit relies on:
// the environment file supplies the defaults and an operator debugging by
// hand overrides one of them on the command line.
func TestFlagsBeatEnvironment(t *testing.T) {
	t.Setenv("SPARKWRANGLER_NODE_ID", "from-env")
	t.Setenv("SPARKWRANGLER_INTERVAL", "45s")
	t.Setenv("SPARKWRANGLER_VLLM_URL", "http://env:8000")

	c, err := Load([]string{"-node-id", "from-flag"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.NodeID != "from-flag" {
		t.Errorf("NodeID = %q, want the flag to win", c.NodeID)
	}
	if c.Interval != 45*time.Second {
		t.Errorf("Interval = %s, want the environment to supply it", c.Interval)
	}
	if c.VLLMURL != "http://env:8000" {
		t.Errorf("VLLMURL = %q, want the environment to supply it", c.VLLMURL)
	}
}

// TestBadEnvironmentFallsBackToDefault pins that an unparseable value
// does not take the daemon down. A typo in an environment file should
// cost a setting, not a node's monitoring.
func TestBadEnvironmentFallsBackToDefault(t *testing.T) {
	t.Setenv("SPARKWRANGLER_INTERVAL", "not-a-duration")
	t.Setenv("SPARKWRANGLER_QOS", "banana")

	c, err := Load([]string{"-node-id", "n"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Interval != 15*time.Second {
		t.Errorf("Interval = %s, want the 15s default", c.Interval)
	}
	if c.QoS != 1 {
		t.Errorf("QoS = %d, want the default 1", c.QoS)
	}
}
