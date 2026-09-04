package vllm

// Metric names this package depends on by exact string.
//
// They are named here, and asserted against a recorded scrape in the
// tests, because getting one wrong fails silently: a metric that is not
// found reads as zero, and zero is also the healthy value for most of
// them. A renamed preemption counter would show a permanently calm
// dashboard on a box that was thrashing.
const (
	// MetricPreemptions is the cumulative count of sequences evicted
	// and recomputed. Spelled num_preemptions_total; the plausible
	// request_num_preemptions_total does not exist.
	MetricPreemptions = "vllm:num_preemptions_total"

	MetricKVCacheUsage     = "vllm:kv_cache_usage_perc"
	MetricRequestsRunning  = "vllm:num_requests_running"
	MetricRequestsWaiting  = "vllm:num_requests_waiting"
	MetricWaitingByReason  = "vllm:num_requests_waiting_by_reason"
	MetricGenerationTokens = "vllm:generation_tokens_total"
	MetricPrefixQueries    = "vllm:prefix_cache_queries_total"
	MetricPrefixHits       = "vllm:prefix_cache_hits_total"
	MetricCacheConfig      = "vllm:cache_config_info"
)
