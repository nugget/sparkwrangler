package publisher

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nugget/sparkrustler/internal/hadiscovery"
	"github.com/nugget/sparkrustler/internal/vllm"
)

var templateKey = regexp.MustCompile(`value_json\.([a-zA-Z0-9_]+)`)

// TestDiscoveryTemplatesMatchStateKeys is the test this package exists
// to make possible. Discovery declares what each entity reads out of the
// state document; State decides what is in it. A disagreement between
// the two — a rename on one side, a typo on the other — produces an
// entity that is permanently unknown, logs nothing, and looks exactly
// like a sensor for something that is not happening.
//
// Both directions matter. A template with no field behind it is a dead
// entity; a field no template reads is either a forgotten entity or dead
// weight in every payload.
func TestDiscoveryTemplatesMatchStateKeys(t *testing.T) {
	t.Parallel()

	stateKeys := populatedStateKeys(t)

	templateKeys := map[string]string{}
	for id, c := range hadiscovery.Sensors("testnode") {
		for _, m := range templateKey.FindAllStringSubmatch(c.ValueTemplate, -1) {
			templateKeys[m[1]] = id
		}
	}

	t.Run("every template reads a field the state publishes", func(t *testing.T) {
		for key, entity := range templateKeys {
			if !stateKeys[key] {
				t.Errorf("entity %q reads value_json.%s, which State never publishes", entity, key)
			}
		}
	})

	t.Run("every published field is read by some entity", func(t *testing.T) {
		for key := range stateKeys {
			if _, ok := templateKeys[key]; !ok {
				t.Errorf("State publishes %q but no entity reads it", key)
			}
		}
	})
}

// populatedStateKeys marshals a State with every optional field set, so
// that omitempty cannot hide a key from the comparison.
func populatedStateKeys(t *testing.T) map[string]bool {
	t.Helper()

	f, i, i64 := 1.0, 1, int64(1)
	s := State{
		VLLMUp:                    true,
		Model:                     "m",
		KVCacheUsagePct:           &f,
		RequestsRunning:           &i,
		RequestsWaiting:           &i,
		WaitingForCapacity:        &i,
		Preemptions:               &f,
		PrefixCacheHitRatePct:     &f,
		GenerationTokensPerSecond: &f,
		MaxModelLen:               &i,
		KVCacheTokens:             &i,
		MaxConcurrency:            &f,
		PrefixCachingEnabled:      true,
		GPUUtilizationPct:         &f,
		GPUClockMHz:               &f,
		GPUTemperatureC:           &f,
		GPUPowerW:                 &f,
		MemoryAvailableBytes:      &i64,
		LastSeen:                  "now",
	}

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal State: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal State: %v", err)
	}
	out := make(map[string]bool, len(decoded))
	for k := range decoded {
		out[k] = true
	}
	return out
}

// TestUniqueIDsAreUnique pins that no two components collide. Home
// Assistant silently drops the second entity sharing a unique id, so a
// copy-paste in the catalog costs a sensor with no error anywhere.
func TestUniqueIDsAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}
	for id, c := range hadiscovery.Sensors("spark-a23e") {
		if c.UniqueID == "" {
			t.Errorf("component %q has no unique id", id)
			continue
		}
		if !strings.HasPrefix(c.UniqueID, "spark-a23e_") {
			t.Errorf("component %q unique id %q is not namespaced by node", id, c.UniqueID)
		}
		if prev, dup := seen[c.UniqueID]; dup {
			t.Errorf("components %q and %q share unique id %q", prev, id, c.UniqueID)
		}
		seen[c.UniqueID] = id
	}
}

// TestDownNodeReportsNothingItCannotKnow pins that a dead engine does not
// publish stale serving numbers. Carrying the last good values forward
// would leave a calm dashboard over an engine that is not running.
func TestDownNodeReportsNothingItCannotKnow(t *testing.T) {
	t.Parallel()

	usage := 0.9
	prev := vllm.Reading{Up: true, GenerationTokens: ptrOf(100.0)}
	s := FromVLLM(vllm.Reading{Up: false, KVCacheUsage: &usage}, &prev, time.Second)

	if s.VLLMUp {
		t.Error("VLLMUp = true for a down reading")
	}
	if s.KVCacheUsagePct != nil {
		t.Errorf("KVCacheUsagePct = %v, want nothing published for a down engine", *s.KVCacheUsagePct)
	}
	if s.GenerationTokensPerSecond != nil {
		t.Error("published a generation rate for a down engine")
	}
}

// TestTokenRateHandlesEngineRestart pins that a counter reset produces no
// reading rather than a negative rate.
func TestTokenRateHandlesEngineRestart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		prev     float64
		curr     float64
		elapsed  time.Duration
		wantRate *float64
	}{
		{name: "a normal interval", prev: 100, curr: 160, elapsed: 10 * time.Second, wantRate: ptrOf(6.0)},
		{name: "a counter reset publishes nothing", prev: 900, curr: 5, elapsed: 10 * time.Second},
		{name: "a zero interval publishes nothing", prev: 100, curr: 160, elapsed: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := vllm.Reading{Up: true, GenerationTokens: &tt.prev}
			curr := vllm.Reading{Up: true, GenerationTokens: &tt.curr}
			got := FromVLLM(curr, &prev, tt.elapsed).GenerationTokensPerSecond

			switch {
			case tt.wantRate == nil && got != nil:
				t.Errorf("rate = %v, want none", *got)
			case tt.wantRate != nil && got == nil:
				t.Errorf("rate = none, want %v", *tt.wantRate)
			case tt.wantRate != nil && *got != *tt.wantRate:
				t.Errorf("rate = %v, want %v", *got, *tt.wantRate)
			}
		})
	}
}

func ptrOf[T any](v T) *T { return &v }
