package hadiscovery

import "fmt"

// Version is stamped into the discovery origin so Home Assistant shows
// which build published a device.
const Version = "0.1.0"

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

// optional renders a value that may be absent from the state payload.
// A missing key would otherwise render as an empty string, which Home
// Assistant stores as a state of "" rather than as unknown; default(None)
// makes an absent reading unavailable, which is what absent means.
func optional(key string) string {
	return fmt.Sprintf("{{ value_json.%s | default(None) }}", key)
}

// Sensors returns the component set for one node, keyed by the object id
// Home Assistant will use.
//
// withVLLM is false for a node that serves no API of its own. On a
// tensor-parallel cluster only the head node does: the others hold half
// the weights and do real work, but have no engine to ask. Declaring the
// serving entities there would leave a dozen permanently-unknown sensors
// and a vLLM indicator stuck off, which reads as a broken node rather
// than a correctly-configured worker.
//
// Every slot the schema offers is filled deliberately. Device classes
// decide unit conversion and long-term statistics; state classes decide
// whether a value is graphable and how it is summed; entity categories
// keep configuration facts out of the way of live readings. An entity
// published without them still works and is worse in every one of those
// respects.
//
// The set is chosen from what an actual incident needed. Queue depth and
// preemptions are here because they distinguish "busy" from
// "oversubscribed"; the prefix cache hit rate is here because it explains
// a latency change nothing else accounts for; available host memory is
// here because on unified memory it is the leading indicator of a wedge.
func Sensors(nodeID string, withVLLM bool) map[string]Component {
	uid := func(suffix string) string { return nodeID + "_" + suffix }

	components := hostSensors(uid)
	if withVLLM {
		for id, component := range vllmSensors(uid) {
			components[id] = component
		}
	}
	// Applied here rather than repeated on every literal, so a component
	// added later cannot forget it and end up named inconsistently with
	// its neighbours.
	for id, component := range components {
		component.HasEntityName = boolPtr(true)
		components[id] = component
	}
	return components
}

// vllmSensors are the components that mean something only on a node
// running the engine.
func vllmSensors(uid func(string) string) map[string]Component {
	return map[string]Component{
		// --- serving state -------------------------------------------------
		"vllm_running": {
			Platform:      "binary_sensor",
			Name:          "vLLM",
			UniqueID:      uid("vllm_running"),
			DeviceClass:   "running",
			ValueTemplate: "{{ 'ON' if value_json.vllm_up else 'OFF' }}",
			PayloadOn:     "ON",
			PayloadOff:    "OFF",
			Icon:          "mdi:server",
		},
		"served_model": {
			Platform:      "sensor",
			Name:          "Served model",
			UniqueID:      uid("served_model"),
			ValueTemplate: optional("model"),
			Icon:          "mdi:brain",
		},
		"kv_cache_usage": {
			Platform:          "sensor",
			Name:              "KV cache usage",
			UniqueID:          uid("kv_cache_usage"),
			ValueTemplate:     optional("kv_cache_usage_pct"),
			UnitOfMeasurement: "%",
			StateClass:        "measurement",
			DisplayPrecision:  intPtr(1),
			Icon:              "mdi:memory",
		},
		"requests_running": {
			Platform:          "sensor",
			Name:              "Requests running",
			UniqueID:          uid("requests_running"),
			ValueTemplate:     optional("requests_running"),
			UnitOfMeasurement: "requests",
			StateClass:        "measurement",
			Icon:              "mdi:play-circle-outline",
		},
		"requests_waiting": {
			Platform:          "sensor",
			Name:              "Requests waiting",
			UniqueID:          uid("requests_waiting"),
			ValueTemplate:     optional("requests_waiting"),
			UnitOfMeasurement: "requests",
			StateClass:        "measurement",
			Icon:              "mdi:timer-sand",
		},
		"waiting_for_capacity": {
			Platform:          "sensor",
			Name:              "Waiting for KV capacity",
			UniqueID:          uid("waiting_for_capacity"),
			ValueTemplate:     optional("waiting_for_capacity"),
			UnitOfMeasurement: "requests",
			StateClass:        "measurement",
			EntityCategory:    "diagnostic",
			Icon:              "mdi:traffic-light-outline",
		},
		"preemptions": {
			// total_increasing, not measurement: it is cumulative, and
			// what matters is the rate, which HA derives correctly only
			// when told the counter can reset.
			Platform:          "sensor",
			Name:              "Preemptions",
			UniqueID:          uid("preemptions"),
			ValueTemplate:     optional("preemptions"),
			UnitOfMeasurement: "events",
			StateClass:        "total_increasing",
			EntityCategory:    "diagnostic",
			Icon:              "mdi:alert-octagon-outline",
		},
		"prefix_cache_hit_rate": {
			Platform:          "sensor",
			Name:              "Prefix cache hit rate",
			UniqueID:          uid("prefix_cache_hit_rate"),
			ValueTemplate:     optional("prefix_cache_hit_rate_pct"),
			UnitOfMeasurement: "%",
			StateClass:        "measurement",
			DisplayPrecision:  intPtr(1),
			Icon:              "mdi:cached",
		},
		"generation_rate": {
			Platform:          "sensor",
			Name:              "Generation rate",
			UniqueID:          uid("generation_rate"),
			ValueTemplate:     optional("generation_tokens_per_second"),
			UnitOfMeasurement: "tok/s",
			StateClass:        "measurement",
			DisplayPrecision:  intPtr(1),
			Icon:              "mdi:speedometer",
		},

		// --- engine configuration ------------------------------------------
		"max_model_len": {
			// No state class: a launch parameter is not a measurement,
			// and giving it one puts a flat line in long-term statistics.
			Platform:          "sensor",
			Name:              "Max model length",
			UniqueID:          uid("max_model_len"),
			ValueTemplate:     optional("max_model_len"),
			UnitOfMeasurement: "tokens",
			EntityCategory:    "diagnostic",
			Icon:              "mdi:arrow-expand-horizontal",
		},
		"kv_cache_tokens": {
			Platform:          "sensor",
			Name:              "KV cache size",
			UniqueID:          uid("kv_cache_tokens"),
			ValueTemplate:     optional("kv_cache_tokens"),
			UnitOfMeasurement: "tokens",
			EntityCategory:    "diagnostic",
			Icon:              "mdi:database-outline",
		},
		"max_concurrency": {
			Platform:          "sensor",
			Name:              "Max concurrency",
			UniqueID:          uid("max_concurrency"),
			ValueTemplate:     optional("max_concurrency"),
			UnitOfMeasurement: "x",
			EntityCategory:    "diagnostic",
			DisplayPrecision:  intPtr(2),
			Icon:              "mdi:arrow-split-vertical",
		},
		"prefix_caching": {
			Platform:       "binary_sensor",
			Name:           "Prefix caching",
			UniqueID:       uid("prefix_caching"),
			ValueTemplate:  "{{ 'ON' if value_json.prefix_caching_enabled else 'OFF' }}",
			PayloadOn:      "ON",
			PayloadOff:     "OFF",
			EntityCategory: "diagnostic",
			Icon:           "mdi:cached",
		},
	}
}

// hostSensors are the components every node publishes, engine or not. A
// tensor-parallel worker is doing the same work as the head node and its
// accelerator and memory readings matter just as much.
func hostSensors(uid func(string) string) map[string]Component {
	return map[string]Component{
		// --- accelerator and host ------------------------------------------
		"gpu_utilization": {
			Platform:          "sensor",
			Name:              "GPU utilisation",
			UniqueID:          uid("gpu_utilization"),
			ValueTemplate:     optional("gpu_utilization_pct"),
			UnitOfMeasurement: "%",
			StateClass:        "measurement",
			Icon:              "mdi:chip",
		},
		"gpu_clock": {
			Platform:          "sensor",
			Name:              "GPU clock",
			UniqueID:          uid("gpu_clock"),
			ValueTemplate:     optional("gpu_clock_mhz"),
			DeviceClass:       "frequency",
			UnitOfMeasurement: "MHz",
			StateClass:        "measurement",
		},
		"gpu_temperature": {
			Platform:          "sensor",
			Name:              "GPU temperature",
			UniqueID:          uid("gpu_temperature"),
			ValueTemplate:     optional("gpu_temperature_c"),
			DeviceClass:       "temperature",
			UnitOfMeasurement: "°C",
			StateClass:        "measurement",
		},
		"gpu_power": {
			Platform:          "sensor",
			Name:              "GPU power",
			UniqueID:          uid("gpu_power"),
			ValueTemplate:     optional("gpu_power_w"),
			DeviceClass:       "power",
			UnitOfMeasurement: "W",
			StateClass:        "measurement",
		},
		"memory_available": {
			// The wedge predictor. On unified memory the GPU and the page
			// cache draw on one pool, so this falling is the earliest
			// visible sign of an over-commit that ends in a power cycle.
			Platform:          "sensor",
			Name:              "Memory available",
			UniqueID:          uid("memory_available"),
			ValueTemplate:     optional("memory_available_bytes"),
			DeviceClass:       "data_size",
			UnitOfMeasurement: "B",
			StateClass:        "measurement",
			Icon:              "mdi:memory",
		},
		"last_seen": {
			Platform:         "sensor",
			Name:             "Last seen",
			UniqueID:         uid("last_seen"),
			ValueTemplate:    optional("last_seen"),
			DeviceClass:      "timestamp",
			EntityCategory:   "diagnostic",
			EnabledByDefault: boolPtr(false),
		},
	}
}
