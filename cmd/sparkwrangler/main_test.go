package main

import (
	"testing"
	"time"

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

	tests := []struct {
		name  string
		state publisher.State
		want  string
	}{
		{
			name:  "a down engine says so plainly",
			state: publisher.State{VLLMUp: false, Model: "stale"},
			want:  "vLLM unreachable",
		},
		{
			name:  "an idle engine",
			state: publisher.State{VLLMUp: true, Model: "qwen", RequestsRunning: i(0), KVCacheUsagePct: f(1.9)},
			want:  "serving qwen, 0 running, KV 1.9%",
		},
		{
			// Queueing is only mentioned when there is some, so the line
			// stays quiet until it has something to say.
			name:  "a queue is surfaced",
			state: publisher.State{VLLMUp: true, Model: "qwen", RequestsRunning: i(4), RequestsWaiting: i(2), KVCacheUsagePct: f(88.5)},
			want:  "serving qwen, 4 running, 2 waiting, KV 88.5%",
		},
		{
			name:  "absent readings are simply omitted",
			state: publisher.State{VLLMUp: true, Model: "qwen"},
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
