package vllm

import (
	"os"
	"strings"
	"testing"
)

// TestMetricNamesExistInLiveScrape guards the failure mode that these
// constants exist for. A wrong metric name does not error — the lookup
// simply misses and the field keeps its zero value, which for a
// preemption counter or a queue depth is also what healthy looks like.
// The fixture is a recorded scrape from a real 2-node vLLM server, so a
// rename upstream fails here rather than on a dashboard that has been
// quietly reporting calm for a week.
func TestMetricNamesExistInLiveScrape(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/live-scrape.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	scrape := string(raw)

	names := []struct{ constant, value string }{
		{"MetricPreemptions", MetricPreemptions},
		{"MetricKVCacheUsage", MetricKVCacheUsage},
		{"MetricRequestsRunning", MetricRequestsRunning},
		{"MetricRequestsWaiting", MetricRequestsWaiting},
		{"MetricWaitingByReason", MetricWaitingByReason},
		{"MetricGenerationTokens", MetricGenerationTokens},
		{"MetricPrefixQueries", MetricPrefixQueries},
		{"MetricPrefixHits", MetricPrefixHits},
		{"MetricCacheConfig", MetricCacheConfig},
	}

	for _, n := range names {
		t.Run(n.constant, func(t *testing.T) {
			if !strings.Contains(scrape, "\n"+n.value+"{") && !strings.Contains(scrape, "\n"+n.value+" ") {
				t.Errorf("%s = %q is not exported by the recorded server; a name changed or was guessed",
					n.constant, n.value)
			}
		})
	}
}

// TestReadingLeavesAbsentMetricsNil pins that a missing metric is
// reported as absent rather than as zero, so a publisher can render it
// unavailable instead of inventing a healthy reading.
func TestReadingLeavesAbsentMetricsNil(t *testing.T) {
	t.Parallel()

	m, err := ParseMetrics(strings.NewReader("vllm:num_requests_running{engine=\"0\"} 2.0\n"))
	if err != nil {
		t.Fatalf("ParseMetrics: %v", err)
	}
	var r Reading
	r.applyMetrics(m)

	if r.RequestsRunning == nil || *r.RequestsRunning != 2 {
		t.Errorf("RequestsRunning = %v, want 2", r.RequestsRunning)
	}
	if r.Preemptions != nil {
		t.Errorf("Preemptions = %v, want nil for a metric the scrape did not carry", *r.Preemptions)
	}
	if r.KVCacheUsage != nil {
		t.Errorf("KVCacheUsage = %v, want nil when absent", *r.KVCacheUsage)
	}
}
