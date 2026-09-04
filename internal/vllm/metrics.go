// Package vllm reads the state of a vLLM server over its HTTP surface:
// liveness, which model is loaded, and the Prometheus metrics it
// exports. Nothing here is specific to a host or an accelerator.
package vllm

import (
	"bufio"
	"io"
	"math"
	"strconv"
	"strings"
)

// Sample is one Prometheus time series: a metric name, its labels, and
// the value from the most recent scrape.
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Metrics is a parsed scrape, indexed by metric name. A name maps to
// every series carrying it, because vLLM labels most series by engine
// and model and a single-model server is only the common case.
type Metrics map[string][]Sample

// ParseMetrics reads the Prometheus text exposition format.
//
// The format is simple enough that parsing it directly costs less than
// the dependency that would do it, and this reads only the subset vLLM
// emits: no exemplars, no timestamps, no escaped label values beyond
// the quoting handled here.
func ParseMetrics(r io.Reader) (Metrics, error) {
	out := Metrics{}
	scanner := bufio.NewScanner(r)
	// Metric lines are short, but a histogram with many buckets and long
	// model names can still outrun the default 64 KiB token.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sample, ok := parseLine(line)
		if !ok {
			continue
		}
		out[sample.Name] = append(out[sample.Name], sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseLine(line string) (Sample, bool) {
	name, rest, ok := splitNameAndRest(line)
	if !ok {
		return Sample{}, false
	}
	labels, value, ok := splitLabelsAndValue(rest)
	if !ok {
		return Sample{}, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return Sample{}, false
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		// NaN is how Prometheus spells "no observations yet", and
		// ParseFloat accepts it rather than failing. Dropped for two
		// reasons: reporting it as 0 would publish a real latency of
		// zero seconds, and encoding/json refuses NaN outright, so one
		// unobserved histogram would fail the whole state payload.
		return Sample{}, false
	}
	return Sample{Name: name, Labels: labels, Value: parsed}, true
}

func splitNameAndRest(line string) (name, rest string, ok bool) {
	if i := strings.IndexAny(line, "{ "); i > 0 {
		return line[:i], line[i:], true
	}
	return "", "", false
}

func splitLabelsAndValue(rest string) (map[string]string, string, bool) {
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "{") {
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return nil, "", false
		}
		return nil, fields[0], true
	}
	end := strings.LastIndex(rest, "}")
	if end < 0 {
		return nil, "", false
	}
	labels := parseLabels(rest[1:end])
	fields := strings.Fields(rest[end+1:])
	if len(fields) == 0 {
		return nil, "", false
	}
	return labels, fields[0], true
}

// parseLabels splits a label set, respecting quoted values so that a
// comma inside one does not end it. vLLM puts model names in labels and
// those carry slashes, dots, and dashes.
func parseLabels(in string) map[string]string {
	out := map[string]string{}
	var key strings.Builder
	var val strings.Builder
	target := &key
	inQuotes := false

	flush := func() {
		if k := strings.TrimSpace(key.String()); k != "" {
			out[k] = val.String()
		}
		key.Reset()
		val.Reset()
		target = &key
	}

	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c == '"' && (i == 0 || in[i-1] != '\\'):
			inQuotes = !inQuotes
		case c == '=' && !inQuotes && target == &key:
			target = &val
		case c == ',' && !inQuotes:
			flush()
		default:
			target.WriteByte(c)
		}
	}
	flush()
	return out
}

// Value returns the first value recorded for a metric name, and whether
// any series carried it. Callers wanting a specific engine or model use
// [Metrics.ValueWhere].
func (m Metrics) Value(name string) (float64, bool) {
	samples := m[name]
	if len(samples) == 0 {
		return 0, false
	}
	return samples[0].Value, true
}

// ValueWhere returns the value of the first series whose labels include
// every pair in match.
func (m Metrics) ValueWhere(name string, match map[string]string) (float64, bool) {
	for _, s := range m[name] {
		matched := true
		for k, v := range match {
			if s.Labels[k] != v {
				matched = false
				break
			}
		}
		if matched {
			return s.Value, true
		}
	}
	return 0, false
}

// Label returns a label from the first series carrying a metric. vLLM
// publishes the whole engine cache configuration this way, as labels on
// a metric whose value is always 1.
func (m Metrics) Label(name, label string) (string, bool) {
	samples := m[name]
	if len(samples) == 0 {
		return "", false
	}
	v, ok := samples[0].Labels[label]
	return v, ok
}
