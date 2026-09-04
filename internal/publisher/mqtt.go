package publisher

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/nugget/sparkwrangler/internal/hadiscovery"
)

const (
	// PayloadOnline and PayloadOffline are the availability values the
	// discovery message declares. The offline one is also the will,
	// which is the entire reason a hard power-off is distinguishable
	// from a quiet node.
	PayloadOnline  = "online"
	PayloadOffline = "offline"
)

// MQTT publishes one node's discovery and state.
type MQTT struct {
	client mqtt.Client
	log    *slog.Logger

	nodeID     string
	stateTopic string
	availTopic string
	discoTopic string
	qos        byte
}

// MQTTOptions configures the transport.
type MQTTOptions struct {
	BrokerURL string
	ClientID  string
	Username  string
	Password  string

	NodeID string
	// DiscoveryPrefix is Home Assistant's, conventionally
	// "homeassistant". TopicPrefix is this daemon's own namespace for
	// state and availability.
	DiscoveryPrefix string
	TopicPrefix     string

	QoS    byte
	Logger *slog.Logger
}

// NewMQTT connects to the broker.
//
// The will is registered before connecting, and it must be: a will set
// afterwards is not part of the session the broker records, so a node
// that loses power publishes nothing and its entities keep the last
// values they had. That is the failure this exists to prevent — a
// dashboard showing a healthy GPU on a machine that is off.
func NewMQTT(opts MQTTOptions) (*MQTT, error) {
	if opts.NodeID == "" {
		return nil, fmt.Errorf("node id is required")
	}
	if opts.DiscoveryPrefix == "" {
		opts.DiscoveryPrefix = "homeassistant"
	}
	if opts.TopicPrefix == "" {
		opts.TopicPrefix = "sparkwrangler"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.ClientID == "" {
		opts.ClientID = "sparkwrangler-" + opts.NodeID
	}

	p := &MQTT{
		log:        opts.Logger.With("node", opts.NodeID),
		nodeID:     opts.NodeID,
		stateTopic: hadiscovery.StateTopic(opts.TopicPrefix, opts.NodeID),
		availTopic: hadiscovery.AvailabilityTopic(opts.TopicPrefix, opts.NodeID),
		discoTopic: hadiscovery.DiscoveryTopic(opts.DiscoveryPrefix, opts.NodeID),
		qos:        opts.QoS,
	}

	co := mqtt.NewClientOptions().
		AddBroker(opts.BrokerURL).
		SetClientID(opts.ClientID).
		SetUsername(opts.Username).
		SetPassword(opts.Password).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(10*time.Second).
		SetMaxReconnectInterval(2*time.Minute).
		SetCleanSession(false).
		SetWill(p.availTopic, PayloadOffline, p.qos, true)

	// Discovery is republished on every reconnect rather than only at
	// start. A broker that lost its retained messages, or a Home
	// Assistant that was reinstalled, otherwise leaves this node
	// publishing state that nothing is listening for.
	co.OnConnect = func(mqtt.Client) {
		p.log.Info("connected to broker")
		if err := p.Announce(); err != nil {
			p.log.Error("announce failed", "error", err)
		}
	}
	co.OnConnectionLost = func(_ mqtt.Client, err error) {
		p.log.Warn("broker connection lost", "error", err)
	}

	p.client = mqtt.NewClient(co)
	token := p.client.Connect()
	if !token.WaitTimeout(30 * time.Second) {
		return nil, fmt.Errorf("connect to %s: timed out", opts.BrokerURL)
	}
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", opts.BrokerURL, err)
	}
	return p, nil
}

// Announce publishes the retained discovery message and marks the node
// online. Retained so Home Assistant recovers the device after its own
// restart without waiting for this daemon's next cycle.
func (p *MQTT) Announce() error {
	cfg := hadiscovery.Config{
		Device:            p.device(),
		Origin:            hadiscovery.Origin{Name: "sparkwrangler", SWVersion: hadiscovery.Version, SupportURL: "https://github.com/nugget/sparkwrangler"},
		Components:        hadiscovery.Sensors(p.nodeID),
		StateTopic:        p.stateTopic,
		AvailabilityTopic: p.availTopic,
		PayloadAvailable:  PayloadOnline,
		PayloadNotAvail:   PayloadOffline,
		QoS:               int(p.qos),
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal discovery: %w", err)
	}
	if err := p.publish(p.discoTopic, payload, true); err != nil {
		return err
	}
	return p.publish(p.availTopic, []byte(PayloadOnline), true)
}

// Device is overridable so a caller can supply hardware identity it
// knows and this package does not.
var Device = hadiscovery.Device{}

func (p *MQTT) device() hadiscovery.Device {
	d := Device
	d.Identifiers = []string{p.nodeID}
	if d.Name == "" {
		d.Name = p.nodeID
	}
	return d
}

// PublishState sends one observation. Retained, so a restarting Home
// Assistant shows real values immediately rather than a device of
// unknowns until the next interval.
func (p *MQTT) PublishState(s State) error {
	payload, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return p.publish(p.stateTopic, payload, true)
}

// Close marks the node offline and disconnects.
//
// The explicit offline is not redundant with the will: a clean shutdown
// does not trigger a will, so without this a deliberately stopped daemon
// would leave its device showing online forever.
func (p *MQTT) Close() {
	if err := p.publish(p.availTopic, []byte(PayloadOffline), true); err != nil {
		p.log.Warn("could not mark offline", "error", err)
	}
	p.client.Disconnect(1000)
}

func (p *MQTT) publish(topic string, payload []byte, retain bool) error {
	token := p.client.Publish(topic, p.qos, retain, payload)
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("publish %s: timed out", topic)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish %s: %w", topic, err)
	}
	return nil
}
