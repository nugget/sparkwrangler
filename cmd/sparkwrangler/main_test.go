package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nugget/sparkwrangler/internal/hadiscovery"
	"github.com/nugget/sparkwrangler/internal/host"
	"github.com/nugget/sparkwrangler/internal/publisher"
)

// TestWatchdogTick pins that the loop runs at whichever cadence is
// tighter. A 15s publish interval under a 10s watchdog deadline would
// miss pings and be killed while perfectly healthy; the loop therefore
// runs at the watchdog's pace and publishes on the cycles that are due.
func TestWatchdogTick(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		interval time.Duration
		watchdog time.Duration
		want     time.Duration
	}{
		{
			name:     "a tighter watchdog sets the pace",
			interval: 15 * time.Second,
			watchdog: 10 * time.Second,
			want:     10 * time.Second,
		},
		{
			name:     "a looser watchdog leaves the interval alone",
			interval: 15 * time.Second,
			watchdog: 30 * time.Second,
			want:     15 * time.Second,
		},
		{
			name:     "no watchdog leaves the interval alone",
			interval: 15 * time.Second,
			want:     15 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := watchdogTick(tt.interval, tt.watchdog); got != tt.want {
				t.Errorf("watchdogTick(%s, %s) = %s, want %s", tt.interval, tt.watchdog, got, tt.want)
			}
		})
	}
}

func TestStatusLine(t *testing.T) {
	t.Parallel()

	i := func(v int) *int { return &v }
	f := func(v float64) *float64 { return &v }
	b := func(v bool) *bool { return &v }

	tests := []struct {
		name  string
		state publisher.State
		want  string
	}{
		{
			// Absent, not false: a worker has no engine to be missing,
			// so "unreachable" would report a fault that does not exist.
			name:  "a worker reports what it does know",
			state: publisher.State{GPUUtilizationPct: f(96)},
			want:  "worker node, GPU 96%",
		},
		{
			name:  "a worker with no accelerator reading says only that",
			state: publisher.State{},
			want:  "worker node",
		},
		{
			name:  "a down engine says so plainly",
			state: publisher.State{VLLMUp: b(false), Model: "stale"},
			want:  "vLLM unreachable",
		},
		{
			name:  "an idle engine",
			state: publisher.State{VLLMUp: b(true), Model: "qwen", RequestsRunning: i(0), KVCacheUsagePct: f(1.9)},
			want:  "serving qwen, 0 running, KV 1.9%",
		},
		{
			// Queueing is only mentioned when there is some, so the line
			// stays quiet until it has something to say.
			name:  "a queue is surfaced",
			state: publisher.State{VLLMUp: b(true), Model: "qwen", RequestsRunning: i(4), RequestsWaiting: i(2), KVCacheUsagePct: f(88.5)},
			want:  "serving qwen, 4 running, 2 waiting, KV 88.5%",
		},
		{
			name:  "absent readings are simply omitted",
			state: publisher.State{VLLMUp: b(true), Model: "qwen"},
			want:  "serving qwen",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusLine(tt.state); got != tt.want {
				t.Errorf("statusLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMACConnections pins the shape of the registry entry, and that an
// unknown address claims nothing. An empty entry would not merely be
// useless, it would assert the connection ("mac", "") and collide with
// any other device that made the same mistake.
func TestMACConnections(t *testing.T) {
	t.Parallel()

	if got := macConnections(""); got != nil {
		t.Errorf("macConnections(\"\") = %v, want no connection claimed", got)
	}

	got := macConnections("aa:bb:cc:00:00:01")
	if len(got) != 1 {
		t.Fatalf("macConnections = %v, want exactly one entry", got)
	}
	if got[0][0] != "mac" {
		t.Errorf("connection type = %q, want %q; Home Assistant matches on this literal", got[0][0], "mac")
	}
	if got[0][1] != "aa:bb:cc:00:00:01" {
		t.Errorf("connection address = %q, want the probed address unaltered", got[0][1])
	}
}

// TestDeviceConnectionsSerialiseAsCns pins the abbreviated discovery key.
// Home Assistant reads "cns" and ignores an unrecognised key in silence,
// so a device published with "connections" spelled out would look
// entirely correct in the payload and record nothing.
func TestDeviceConnectionsSerialiseAsCns(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(hadiscovery.Device{
		Name:        "spark-01",
		Connections: macConnections("aa:bb:cc:00:00:01"),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cns, ok := decoded["cns"]
	if !ok {
		t.Fatalf("device published no \"cns\" key: %s", raw)
	}
	pairs, ok := cns.([]any)
	if !ok || len(pairs) != 1 {
		t.Fatalf("cns = %v, want a list of one pair", cns)
	}
	pair, ok := pairs[0].([]any)
	if !ok || len(pair) != 2 || pair[0] != "mac" {
		t.Errorf("cns[0] = %v, want [\"mac\", <address>]", pairs[0])
	}

	// A node with no address must publish no key at all rather than an
	// empty list, which Home Assistant would treat as a device asserting
	// it has no connections.
	raw, err = json.Marshal(hadiscovery.Device{Name: "spark-01"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(raw, []byte(`"cns"`)) {
		t.Errorf("device with no MAC published a connections key: %s", raw)
	}
}

// TestExplicitInterfaceFailureStopsStartup pins that a named interface is
// a promise rather than a preference. A typo in -net-interface used to be
// logged and absorbed, which left the daemon running and publishing no
// address at all — the operator's evidence that they had asked for one
// being a single WARN line in the journal.
//
// The broker address is deliberately one nothing answers on: a run that
// wrongly continues past the MAC failure fails later for a different
// reason, and the assertion on the message tells the two apart.
func TestExplicitInterfaceFailureStopsStartup(t *testing.T) {
	original := host.SysClassNet
	t.Cleanup(func() { host.SysClassNet = original })
	host.SysClassNet = t.TempDir()

	err := run([]string{
		"-node-id", "spark-01",
		"-net-interface", "enp9s0",
		"-vllm-url", "",
		"-broker", "tcp://127.0.0.1:1",
	})
	if err == nil {
		t.Fatal("run succeeded with an unreadable -net-interface")
	}
	if !strings.Contains(err.Error(), "enp9s0") {
		t.Errorf("error = %v, want one naming the interface the operator asked for", err)
	}
}
