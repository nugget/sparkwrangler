package vllm

import (
	"strings"
	"testing"
)

// Lines taken verbatim from a live vLLM 2-node server so the parser is
// tested against the exposition this actually consumes, including the
// cache config line whose labels carry slashes, dots and a bare "True".
const liveScrape = `# HELP vllm:num_requests_running Number of requests in model execution batches.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{engine="0",model_name="Qwen/Qwen3.5-122B-A10B-FP8"} 1.0
vllm:num_requests_waiting{engine="0",model_name="Qwen/Qwen3.5-122B-A10B-FP8"} 0.0
vllm:num_requests_waiting_by_reason{engine="0",model_name="Qwen/Qwen3.5-122B-A10B-FP8",reason="capacity"} 3.0
vllm:kv_cache_usage_perc{engine="0",model_name="Qwen/Qwen3.5-122B-A10B-FP8"} 0.018907563025210128
vllm:prefix_cache_queries_total{engine="0",model_name="Qwen/Qwen3.5-122B-A10B-FP8"} 1.012506e+06
vllm:prefix_cache_hits_total{engine="0",model_name="Qwen/Qwen3.5-122B-A10B-FP8"} 492560.0
vllm:cache_config_info{block_size="2096",enable_prefix_caching="True",kv_cache_size_tokens="1892600",kv_cache_max_concurrency="7.21969696969697"} 1.0
process_resident_memory_bytes 1.2345e+09
vllm:time_to_first_token_seconds_sum{engine="0"} NaN
`

func TestParseMetrics(t *testing.T) {
	t.Parallel()

	m, err := ParseMetrics(strings.NewReader(liveScrape))
	if err != nil {
		t.Fatalf("ParseMetrics: %v", err)
	}

	t.Run("values", func(t *testing.T) {
		tests := []struct {
			name   string
			metric string
			want   float64
		}{
			{name: "a plain gauge", metric: "vllm:num_requests_running", want: 1},
			{name: "a fractional gauge keeps its precision", metric: "vllm:kv_cache_usage_perc", want: 0.018907563025210128},
			{name: "scientific notation", metric: "vllm:prefix_cache_queries_total", want: 1012506},
			{name: "a series with no labels at all", metric: "process_resident_memory_bytes", want: 1.2345e+09},
		}
		for _, tt := range tests {
			got, ok := m.Value(tt.metric)
			if !ok {
				t.Errorf("%s: %s missing", tt.name, tt.metric)
				continue
			}
			if got != tt.want {
				t.Errorf("%s: %s = %v, want %v", tt.name, tt.metric, got, tt.want)
			}
		}
	})

	t.Run("a NaN is absent rather than zero", func(t *testing.T) {
		// Reading it as 0 would report a real latency of zero seconds,
		// which is worse than reporting nothing.
		if _, ok := m.Value("vllm:time_to_first_token_seconds_sum"); ok {
			t.Error("NaN parsed as a reading; want it dropped")
		}
	})

	t.Run("labels select among series", func(t *testing.T) {
		got, ok := m.ValueWhere("vllm:num_requests_waiting_by_reason", map[string]string{"reason": "capacity"})
		if !ok || got != 3 {
			t.Errorf("waiting_by_reason[capacity] = %v (ok=%v), want 3", got, ok)
		}
		if _, ok := m.ValueWhere("vllm:num_requests_running", map[string]string{"engine": "9"}); ok {
			t.Error("matched a series whose label does not match")
		}
	})

	t.Run("config labels survive slashes, dots and commas", func(t *testing.T) {
		tests := []struct{ label, want string }{
			{"kv_cache_size_tokens", "1892600"},
			{"kv_cache_max_concurrency", "7.21969696969697"},
			{"enable_prefix_caching", "True"},
			{"block_size", "2096"},
		}
		for _, tt := range tests {
			got, ok := m.Label("vllm:cache_config_info", tt.label)
			if !ok || got != tt.want {
				t.Errorf("cache_config_info[%s] = %q (ok=%v), want %q", tt.label, got, ok, tt.want)
			}
		}
		// The model name is the value most likely to break a naive
		// split: it carries a slash and lives beside other labels.
		if got, _ := m.Label("vllm:num_requests_running", "model_name"); got != "Qwen/Qwen3.5-122B-A10B-FP8" {
			t.Errorf("model_name = %q, want the full path", got)
		}
	})
}
