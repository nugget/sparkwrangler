package publisher

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nugget/sparkwrangler/internal/hadiscovery"
	"github.com/nugget/sparkwrangler/internal/vllm"
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
	for id, c := range hadiscovery.Sensors("testnode", true) {
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
		VLLMUp:                    ptrOf(true),
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
		PrefixCachingEnabled:      ptrOf(true),
		GPUUtilizationPct:         &f,
		GPUClockMHz:               &f,
		GPUTemperatureC:           &f,
		GPUPowerW:                 &f,
		MemoryAvailableBytes:      &i64,
		MemoryUsedPct:             &f,
		MACAddress:                "02:00:00:00:00:01",
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
	for id, c := range hadiscovery.Sensors("spark-01", true) {
		if c.UniqueID == "" {
			t.Errorf("component %q has no unique id", id)
			continue
		}
		if !strings.HasPrefix(c.UniqueID, "spark-01_") {
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

	if s.VLLMUp == nil || *s.VLLMUp {
		t.Errorf("VLLMUp = %v, want an explicit false for a configured but unreachable engine", s.VLLMUp)
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

// TestWorkerModeDeclaresNoServingEntities pins what worker mode is for.
// On a tensor-parallel cluster only the head node serves the API, so a
// worker declaring the serving entities leaves a dozen permanently
// unknown sensors and a vLLM indicator stuck off — which reads as a
// broken node rather than a correctly configured one.
func TestWorkerModeDeclaresNoServingEntities(t *testing.T) {
	t.Parallel()

	head := hadiscovery.Sensors("spark-01", true)
	worker := hadiscovery.Sensors("spark-01", false)

	if len(worker) >= len(head) {
		t.Fatalf("worker declares %d components and head %d; want strictly fewer", len(worker), len(head))
	}

	// The accelerator and host readings matter just as much on a worker:
	// it is doing the same work, on the same silicon.
	for _, id := range []string{"gpu_utilization", "gpu_clock", "gpu_temperature", "gpu_power", "memory_available", "memory_used", "mac_address", "last_seen"} {
		if _, ok := worker[id]; !ok {
			t.Errorf("worker is missing %q, which has nothing to do with serving", id)
		}
	}

	for _, id := range []string{"vllm_running", "served_model", "kv_cache_usage", "requests_running", "preemptions", "prefix_cache_hit_rate", "max_model_len"} {
		if _, ok := worker[id]; ok {
			t.Errorf("worker declares %q, which it can never report", id)
		}
		if _, ok := head[id]; !ok {
			t.Errorf("head node is missing %q", id)
		}
	}
}

// TestWorkerStatePublishesNothingAboutServing pins the payload half of
// the same contract. Absent rather than false: false means the engine
// should be here and is not, and a worker has no engine to be missing.
func TestWorkerStatePublishesNothingAboutServing(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(WorkerState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"vllm_up", "model", "kv_cache_usage_pct", "prefix_caching_enabled", "preemptions"} {
		if _, present := decoded[key]; present {
			t.Errorf("worker state publishes %q, which claims something about an engine it does not run", key)
		}
	}
	if _, present := decoded["last_seen"]; !present {
		t.Error("worker state has no last_seen; the node still needs to say it is alive")
	}
}

// TestEveryComponentComposesItsName pins has_entity_name across the
// catalog. Without it a sensor called "KV cache usage" is called exactly
// that on every node, so two nodes produce two identically named
// entities and neither says which machine it came from.
func TestEveryComponentComposesItsName(t *testing.T) {
	t.Parallel()

	for _, withVLLM := range []bool{true, false} {
		for id, c := range hadiscovery.Sensors("spark-01", withVLLM) {
			if c.HasEntityName == nil || !*c.HasEntityName {
				t.Errorf("component %q (withVLLM=%v) does not set has_entity_name", id, withVLLM)
			}
		}
	}
}

// TestUnobservedPrefixCachingIsAbsentNotFalse pins the third instance of
// this project's one recurring bug. The engine reports prefix caching in
// a cache_config_info label; when /metrics is unreachable or the label is
// missing, a plain bool stays false and gets republished as though
// somebody had looked. False here means prefix caching is switched off,
// which is a materially different claim from nobody having said.
func TestUnobservedPrefixCachingIsAbsentNotFalse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		observed    *bool
		wantPresent bool
		wantValue   bool
	}{
		{name: "reported on", observed: ptrOf(true), wantPresent: true, wantValue: true},
		{name: "reported off", observed: ptrOf(false), wantPresent: true, wantValue: false},
		{name: "never reported", observed: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := FromVLLM(vllm.Reading{Up: true, Model: "m", PrefixCachingOn: tt.observed}, nil, 0)

			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			got, present := decoded["prefix_caching_enabled"]
			if present != tt.wantPresent {
				t.Fatalf("present = %v, want %v (value %v)", present, tt.wantPresent, got)
			}
			if tt.wantPresent && got != tt.wantValue {
				t.Errorf("prefix_caching_enabled = %v, want %v", got, tt.wantValue)
			}
		})
	}
}

// TestBinarySensorsDistinguishMissingFromFalse pins the template half.
// Omitting a key achieves nothing on its own: an undefined key is falsey
// in Jinja, so the obvious template renders OFF for a reading nobody
// took and the sensor reports the opposite of unknown with confidence.
//
// Checked across every binary_sensor rather than the two that exist
// today, so a sensor added later cannot reintroduce it.
func TestBinarySensorsDistinguishMissingFromFalse(t *testing.T) {
	t.Parallel()

	for id, c := range hadiscovery.Sensors("spark-01", true) {
		if c.Platform != "binary_sensor" {
			continue
		}
		t.Run(id, func(t *testing.T) {
			if !strings.Contains(c.ValueTemplate, "is not defined") {
				t.Errorf("binary sensor %q renders an undefined key as OFF rather than unavailable:\n  %s",
					id, c.ValueTemplate)
			}
			// The definedness check has to come before the truth test,
			// or the falsey undefined value is consumed first.
			defined := strings.Index(c.ValueTemplate, "is not defined")
			on := strings.Index(c.ValueTemplate, "'ON'")
			if on >= 0 && defined > on {
				t.Errorf("binary sensor %q tests truth before definedness:\n  %s", id, c.ValueTemplate)
			}
		})
	}
}
