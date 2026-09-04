// Package publisher turns node observations into the JSON state payload
// the Home Assistant components read.
package publisher

import (
	"time"

	"github.com/nugget/sparkwrangler/internal/vllm"
)

// State is the single JSON document published to a node's state topic.
//
// Every field the discovery templates reference appears here by exactly
// the name they use; StateKeys and the discovery catalog are checked
// against each other in tests, because a mismatch produces an entity
// that is permanently unknown and reports no error anywhere.
//
// Optional fields are pointers and are omitted when absent, which the
// templates render as unavailable rather than as a fabricated zero.
type State struct {
	// VLLMUp is absent on a node that serves no engine, rather than
	// false. False means "the engine should be here and is not"; a
	// tensor-parallel worker has no engine to be missing, and publishing
	// false there would report a fault that does not exist.
	VLLMUp *bool  `json:"vllm_up,omitempty"`
	Model  string `json:"model,omitempty"`

	KVCacheUsagePct           *float64 `json:"kv_cache_usage_pct,omitempty"`
	RequestsRunning           *int     `json:"requests_running,omitempty"`
	RequestsWaiting           *int     `json:"requests_waiting,omitempty"`
	WaitingForCapacity        *int     `json:"waiting_for_capacity,omitempty"`
	Preemptions               *float64 `json:"preemptions,omitempty"`
	PrefixCacheHitRatePct     *float64 `json:"prefix_cache_hit_rate_pct,omitempty"`
	GenerationTokensPerSecond *float64 `json:"generation_tokens_per_second,omitempty"`

	MaxModelLen    *int     `json:"max_model_len,omitempty"`
	KVCacheTokens  *int     `json:"kv_cache_tokens,omitempty"`
	MaxConcurrency *float64 `json:"max_concurrency,omitempty"`
	// Absent on a worker for the same reason as VLLMUp: it is a property
	// of an engine, and there is no engine here to have it.
	PrefixCachingEnabled *bool `json:"prefix_caching_enabled,omitempty"`

	GPUUtilizationPct    *float64 `json:"gpu_utilization_pct,omitempty"`
	GPUClockMHz          *float64 `json:"gpu_clock_mhz,omitempty"`
	GPUTemperatureC      *float64 `json:"gpu_temperature_c,omitempty"`
	GPUPowerW            *float64 `json:"gpu_power_w,omitempty"`
	MemoryAvailableBytes *int64   `json:"memory_available_bytes,omitempty"`

	LastSeen string `json:"last_seen"`
}

// WorkerState is the starting state for a node that runs no engine. The
// caller fills the accelerator and host readings onto it exactly as it
// would for a serving node; the difference is only that nothing claims
// anything about an engine.
func WorkerState() State {
	return State{LastSeen: time.Now().UTC().Format(time.RFC3339)}
}

// FromVLLM fills the serving half of the state from one reading. The
// host and accelerator half is filled by the platform adapter.
//
// prev and elapsed carry the previous reading so a cumulative token
// counter becomes a rate; a dashboard wants tokens per second, and the
// total since the engine started is not that.
func FromVLLM(r vllm.Reading, prev *vllm.Reading, elapsed time.Duration) State {
	s := State{
		VLLMUp:               &r.Up,
		Model:                r.Model,
		PrefixCachingEnabled: &r.PrefixCachingOn,
		LastSeen:             time.Now().UTC().Format(time.RFC3339),
	}
	if !r.Up {
		// Nothing below is knowable when the server is down, and
		// carrying the last good values forward would show a calm
		// dashboard for a dead engine.
		return s
	}

	if r.MaxModelLen > 0 {
		s.MaxModelLen = &r.MaxModelLen
	}
	if r.KVCacheTokens > 0 {
		s.KVCacheTokens = &r.KVCacheTokens
	}
	if r.MaxConcurrency > 0 {
		s.MaxConcurrency = &r.MaxConcurrency
	}

	s.RequestsRunning = r.RequestsRunning
	s.RequestsWaiting = r.RequestsWaiting
	s.WaitingForCapacity = r.WaitingForCapacity
	s.Preemptions = r.Preemptions

	if r.KVCacheUsage != nil {
		s.KVCacheUsagePct = pct(*r.KVCacheUsage)
	}
	if r.PrefixCacheHitRate != nil {
		s.PrefixCacheHitRatePct = pct(*r.PrefixCacheHitRate)
	}

	if rate, ok := tokenRate(r, prev, elapsed); ok {
		s.GenerationTokensPerSecond = &rate
	}
	return s
}

// tokenRate derives tokens per second from two cumulative readings. A
// counter that went backwards means the engine restarted between
// scrapes, and no rate is reported for that interval rather than a
// negative one.
func tokenRate(r vllm.Reading, prev *vllm.Reading, elapsed time.Duration) (float64, bool) {
	if prev == nil || r.GenerationTokens == nil || prev.GenerationTokens == nil {
		return 0, false
	}
	if elapsed <= 0 {
		return 0, false
	}
	delta := *r.GenerationTokens - *prev.GenerationTokens
	if delta < 0 {
		return 0, false
	}
	return delta / elapsed.Seconds(), true
}

func pct(fraction float64) *float64 {
	v := fraction * 100
	return &v
}
