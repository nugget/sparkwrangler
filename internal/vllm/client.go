package vllm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Client reads one vLLM server. It is deliberately read-only: this
// package observes, and nothing here can change what the server is
// doing.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a client for a vLLM base URL such as
// http://localhost:8000.
func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

// Reading is one observation of a vLLM server.
//
// The fields are chosen from what a real incident needed, not from what
// the exposition happens to contain: whether it is up, what it is
// serving, how close the KV cache is to the wall, whether work is
// queueing, and whether the engine has begun preempting. Everything
// else vLLM exports is available through the raw metrics but is noise
// on a dashboard.
type Reading struct {
	// Up is whether /health answered. Everything below is meaningless
	// when it is false, and the publisher says so rather than holding
	// the last good values.
	Up bool

	// Model is the served model id, and MaxModelLen the context length
	// it was launched with — the number that silently costs money when
	// a client's configuration disagrees with it.
	Model       string
	MaxModelLen int

	// KVCacheUsage is the fraction of the KV cache in use, 0 to 1. The
	// leading indicator of preemption on a box serving long contexts.
	KVCacheUsage *float64

	// KVCacheTokens is the pool size the engine computed at startup,
	// and MaxConcurrency how many full-length sequences that allows.
	KVCacheTokens  int
	MaxConcurrency float64

	// RequestsRunning and RequestsWaiting are the queue. Waiting above
	// zero on a single-user deployment means something is wrong.
	RequestsRunning *int
	RequestsWaiting *int

	// WaitingForCapacity separates "queued because the KV cache is
	// full" from other queueing, which is the difference between
	// oversubscribed and merely busy.
	WaitingForCapacity *int

	// Preemptions counts sequences evicted and recomputed. Non-zero
	// means the pool is genuinely too small for the offered load, and
	// it is monotonic, so a rate matters more than the total.
	Preemptions *float64

	// PrefixCacheHitRate is hits over queries since start, 0 to 1. On a
	// conversational workload this is most of why repeat turns are
	// cheap, and a collapse in it explains a latency complaint that
	// nothing else accounts for.
	PrefixCacheHitRate *float64
	// PrefixCachingOn is absent when the engine did not report it —
	// /metrics unreachable, or the cache_config_info label missing. A
	// plain bool would publish a fabricated false, and false here means
	// "prefix caching is off", which is a materially different claim
	// from "nobody said".
	PrefixCachingOn *bool

	// GenerationTokens is a monotonic counter; the publisher derives a
	// rate from successive readings rather than reporting the total.
	GenerationTokens *float64

	// Raw is the full scrape, kept so a future sensor needs no change
	// here.
	Raw Metrics
}

// Read collects one observation. A server that fails its health check
// returns a Reading with Up false and no error: down is a state to
// publish, not a failure to fetch.
func (c *Client) Read(ctx context.Context) (Reading, error) {
	var r Reading

	if !c.healthy(ctx) {
		return r, nil
	}
	r.Up = true

	if model, maxLen, err := c.servedModel(ctx); err == nil {
		r.Model = model
		r.MaxModelLen = maxLen
	}

	m, err := c.metrics(ctx)
	if err != nil {
		// Up but unscrapable is worth reporting as up: /health answering
		// means it can still serve, and hiding that because a metrics
		// endpoint moved would be its own false alarm.
		return r, nil
	}
	r.Raw = m
	r.applyMetrics(m)
	return r, nil
}

func (r *Reading) applyMetrics(m Metrics) {
	if v, ok := m.Value("vllm:kv_cache_usage_perc"); ok {
		r.KVCacheUsage = ptr(v)
	}
	if v, ok := m.Value("vllm:num_requests_running"); ok {
		r.RequestsRunning = ptr(int(v))
	}
	if v, ok := m.Value("vllm:num_requests_waiting"); ok {
		r.RequestsWaiting = ptr(int(v))
	}
	if v, ok := m.ValueWhere("vllm:num_requests_waiting_by_reason", map[string]string{"reason": "capacity"}); ok {
		r.WaitingForCapacity = ptr(int(v))
	}
	if v, ok := m.Value(MetricPreemptions); ok {
		r.Preemptions = ptr(v)
	}
	if v, ok := m.Value("vllm:generation_tokens_total"); ok {
		r.GenerationTokens = ptr(v)
	}

	queries, qok := m.Value("vllm:prefix_cache_queries_total")
	hits, hok := m.Value("vllm:prefix_cache_hits_total")
	if qok && hok && queries > 0 {
		r.PrefixCacheHitRate = ptr(hits / queries)
	}

	if v, ok := m.Label(MetricCacheConfig, "enable_prefix_caching"); ok {
		r.PrefixCachingOn = ptr(v == "True" || v == "true")
	}
	if v, ok := m.Label("vllm:cache_config_info", "kv_cache_size_tokens"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			r.KVCacheTokens = n
		}
	}
	if v, ok := m.Label("vllm:cache_config_info", "kv_cache_max_concurrency"); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			r.MaxConcurrency = f
		}
	}
}

func ptr[T any](v T) *T { return &v }

func (c *Client) healthy(ctx context.Context) bool {
	resp, err := c.get(ctx, "/health")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func (c *Client) servedModel(ctx context.Context) (string, int, error) {
	resp, err := c.get(ctx, "/v1/models")
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", 0, err
	}
	if len(payload.Data) == 0 {
		return "", 0, fmt.Errorf("no models served")
	}
	return payload.Data[0].ID, payload.Data[0].MaxModelLen, nil
}

func (c *Client) metrics(ctx context.Context) (Metrics, error) {
	resp, err := c.get(ctx, "/metrics")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics: status %d", resp.StatusCode)
	}
	return ParseMetrics(resp.Body)
}

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}
