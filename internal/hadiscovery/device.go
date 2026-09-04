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
	Identifiers  []string `json:"ids"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"mf,omitempty"`
	Model        string   `json:"mdl,omitempty"`
	ModelID      string   `json:"mdl_id,omitempty"`
	SWVersion    string   `json:"sw,omitempty"`
	HWVersion    string   `json:"hw,omitempty"`
	SerialNumber string   `json:"sn,omitempty"`
	ConfigURL    string   `json:"cu,omitempty"`
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
	Platform          string `json:"p"`
	Name              string `json:"name"`
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

// Topics for a node. Discovery is retained so Home Assistant recovers
// the device after its own restart without waiting for a publish cycle;
// state is retained for the same reason.
func DiscoveryTopic(discoveryPrefix, nodeID string) string {
	return fmt.Sprintf("%s/device/%s/config", discoveryPrefix, nodeID)
}

func StateTopic(basePrefix, nodeID string) string {
	return fmt.Sprintf("%s/%s/state", basePrefix, nodeID)
}

func AvailabilityTopic(basePrefix, nodeID string) string {
	return fmt.Sprintf("%s/%s/availability", basePrefix, nodeID)
}
