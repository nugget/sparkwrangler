// Package hadiscovery builds Home Assistant MQTT discovery payloads.
//
// It emits device-based discovery: one retained message on
// homeassistant/device/<id>/config declares the node and every sensor
// hanging off it, rather than one message per entity. That form arrived
// in HA 2024.11 and is the shape the 2026 releases build on. It matters
// here for more than tidiness — the components share one state topic,
// one availability topic and one device record, so a node appears and
// disappears as a unit instead of as a drift of orphaned entities.
package hadiscovery

import "fmt"

// Device is the physical node, as Home Assistant will file it.
type Device struct {
	Identifiers []string `json:"ids"`
	// Connections is how Home Assistant folds this device together with
	// the one another integration already has for the same machine. Each
	// entry is a [type, identifier] pair; the type that matters here is
	// "mac", which the DHCP, router and device-tracker integrations all
	// register their devices under.
	//
	// This, not the MAC sensor, is the mechanism. An entity holding the
	// address is something an operator can read; a matching connection
	// is what makes the two device records one, so the vLLM sensors and
	// whatever else knows this machine end up on a single page.
	Connections  [][2]string `json:"cns,omitempty"`
	Name         string      `json:"name"`
	Manufacturer string      `json:"mf,omitempty"`
	Model        string      `json:"mdl,omitempty"`
	ModelID      string      `json:"mdl_id,omitempty"`
	SWVersion    string      `json:"sw,omitempty"`
	HWVersion    string      `json:"hw,omitempty"`
	SerialNumber string      `json:"sn,omitempty"`
	ConfigURL    string      `json:"cu,omitempty"`
}

// Origin names the software that published the discovery, which is what
// Home Assistant shows as the integration behind the device.
type Origin struct {
	Name       string `json:"name"`
	SWVersion  string `json:"sw,omitempty"`
	SupportURL string `json:"url,omitempty"`
}

// Component is one entity on the device. The JSON tags use Home
// Assistant's abbreviated discovery keys, which are not optional: the
// long forms are accepted for some keys and silently ignored for
// others, and the abbreviations are what the schema documents.
type Component struct {
	Platform string `json:"p"`
	Name     string `json:"name"`
	// HasEntityName tells Home Assistant that Name is the entity's own
	// name, to be composed with the device's. Without it a sensor called
	// "KV cache usage" is called that on every node, and two nodes give
	// two identically-named entities; with it they become "spark-01 KV
	// cache usage" and so on. Spelled in full because Home Assistant's
	// abbreviation table has no short form for it.
	HasEntityName     *bool  `json:"has_entity_name,omitempty"`
	UniqueID          string `json:"uniq_id"`
	ValueTemplate     string `json:"val_tpl,omitempty"`
	DeviceClass       string `json:"dev_cla,omitempty"`
	StateClass        string `json:"stat_cla,omitempty"`
	UnitOfMeasurement string `json:"unit_of_meas,omitempty"`
	Icon              string `json:"ic,omitempty"`
	EntityCategory    string `json:"ent_cat,omitempty"`
	DisplayPrecision  *int   `json:"sug_dsp_prc,omitempty"`
	PayloadOn         string `json:"pl_on,omitempty"`
	PayloadOff        string `json:"pl_off,omitempty"`
	EnabledByDefault  *bool  `json:"en,omitempty"`
}

// Config is the single retained discovery message.
type Config struct {
	Device            Device               `json:"dev"`
	Origin            Origin               `json:"o"`
	Components        map[string]Component `json:"cmps"`
	StateTopic        string               `json:"stat_t"`
	AvailabilityTopic string               `json:"avty_t"`
	PayloadAvailable  string               `json:"pl_avail"`
	PayloadNotAvail   string               `json:"pl_not_avail"`
	QoS               int                  `json:"qos"`
}

// DiscoveryTopic returns the topic carrying a node's device-discovery
// message, under Home Assistant's discovery prefix (conventionally
// "homeassistant"). Publish to it retained, so Home Assistant recovers
// the device after its own restart rather than waiting for the next
// publish cycle.
func DiscoveryTopic(discoveryPrefix, nodeID string) string {
	return fmt.Sprintf("%s/device/%s/config", discoveryPrefix, nodeID)
}

// StateTopic returns the topic carrying a node's JSON state document.
// Every component declared in the discovery message reads from this one
// topic, selecting its own field with a value template. Publish retained
// for the same reason as discovery.
func StateTopic(basePrefix, nodeID string) string {
	return fmt.Sprintf("%s/%s/state", basePrefix, nodeID)
}

// AvailabilityTopic returns the topic carrying a node's online or
// offline marker. This is the topic a will is registered against, so a
// node that loses power is reported offline by the broker rather than
// leaving its entities frozen at their last healthy values.
func AvailabilityTopic(basePrefix, nodeID string) string {
	return fmt.Sprintf("%s/%s/availability", basePrefix, nodeID)
}
