// Command sparkwrangler publishes the state of one vLLM node to Home
// Assistant over MQTT.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nugget/sparkwrangler/internal/config"
	"github.com/nugget/sparkwrangler/internal/gpu"
	"github.com/nugget/sparkwrangler/internal/hadiscovery"
	"github.com/nugget/sparkwrangler/internal/host"
	"github.com/nugget/sparkwrangler/internal/publisher"
	"github.com/nugget/sparkwrangler/internal/sdnotify"
	"github.com/nugget/sparkwrangler/internal/vllm"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "sparkwrangler:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	// Probed once, before the device record is built: the connection has
	// to be in the retained discovery message, which is published from
	// the broker's connect callback inside NewMQTT below. A MAC found
	// later would never reach Home Assistant on this run.
	mac, err := host.PrimaryMAC(cfg.NetInterface)
	if err != nil {
		log.Warn("could not read the ethernet MAC", "interface", cfg.NetInterface, "error", err)
	}
	if mac == "" {
		log.Debug("no ethernet MAC to publish; the device will not link to others for this machine")
	}

	publisher.Device = hadiscovery.Device{
		Name: firstNonEmpty(cfg.NodeName, cfg.NodeID),
		// The connection, not the sensor, is what makes Home Assistant
		// treat this device and whatever its DHCP or router integration
		// knows as one machine. Omitted entirely when unknown, because a
		// wrong address links this node's sensors onto somebody else's
		// device page.
		Connections:  macConnections(mac),
		Manufacturer: "NVIDIA",
		Model:        cfg.DeviceModel,
		SWVersion:    hadiscovery.Version,
		// Deliberately not cfg.VLLMURL: that is loopback on every normal
		// deployment, and Home Assistant renders it as a link, which
		// would send whoever clicks it to their own machine.
		ConfigURL: cfg.ConfigURL,
	}

	mq, err := publisher.NewMQTT(publisher.MQTTOptions{
		BrokerURL:       cfg.BrokerURL,
		ClientID:        cfg.ClientID,
		Username:        cfg.Username,
		Password:        cfg.Password,
		NodeID:          cfg.NodeID,
		DiscoveryPrefix: cfg.DiscoveryPrefix,
		TopicPrefix:     cfg.TopicPrefix,
		QoS:             byte(cfg.QoS),
		WithVLLM:        !cfg.WorkerMode(),
		TLS: publisher.TLSOptions{
			CAFile:     cfg.CAFile,
			CertFile:   cfg.CertFile,
			KeyFile:    cfg.KeyFile,
			ServerName: cfg.TLSServer,
			Insecure:   cfg.TLSInsecure,
		},
		Logger: log,
	})
	if err != nil {
		return err
	}
	defer mq.Close()

	sd := sdnotify.New()
	defer func() { _ = sd.Close() }()
	// Announced here rather than at the top of main: readiness under
	// Type=notify means the broker connection exists, which is the only
	// state in which this service does anything for anyone.
	if err := sd.Ready(); err != nil {
		log.Warn("could not signal readiness", "error", err)
	}

	// SIGTERM as well as SIGINT: systemd sends the former, and without
	// it a stopped unit would skip the offline publish and leave its
	// device showing online until the will eventually fired.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("publishing",
		"node", cfg.NodeID,
		"vllm", firstNonEmpty(cfg.VLLMURL, "none (worker mode)"),
		"broker", cfg.BrokerURL,
		"interval", cfg.Interval,
		"mac", firstNonEmpty(mac, "none"),
		"watchdog", sdnotify.WatchdogInterval(),
		"systemd", sd.Enabled(),
	)
	return poll(ctx, cfg, mq, sd, mac, log)
}

// macConnections renders the device-registry connection list, or nil
// when there is no address to claim.
func macConnections(mac string) [][2]string {
	if mac == "" {
		return nil
	}
	return [][2]string{{"mac", mac}}
}

// watchdogTick returns the ticker period for the poll loop.
//
// When systemd's watchdog is enabled and its deadline is tighter than
// the publish interval, the loop runs at the watchdog's pace and
// publishes only on the cycles that are due. The alternative — pinging
// from a separate goroutine — would keep a service alive precisely when
// its poll loop had wedged, which is the failure the watchdog exists to
// catch.
func watchdogTick(interval, watchdog time.Duration) time.Duration {
	if watchdog > 0 && watchdog < interval {
		return watchdog
	}
	return interval
}

// statusLine is what systemctl status shows. It answers the question an
// operator opening it actually has, which is never "is the process
// running" — systemd already said that — but "is it getting anywhere".
func statusLine(s publisher.State) string {
	if s.VLLMUp == nil {
		// A worker has nothing to say about serving, so it reports the
		// thing it does know rather than a blank or a false alarm.
		if s.GPUUtilizationPct != nil {
			return fmt.Sprintf("worker node, GPU %.0f%%", *s.GPUUtilizationPct)
		}
		return "worker node"
	}
	if !*s.VLLMUp {
		return "vLLM unreachable"
	}
	line := "serving " + s.Model
	if s.RequestsRunning != nil {
		line += fmt.Sprintf(", %d running", *s.RequestsRunning)
	}
	if s.RequestsWaiting != nil && *s.RequestsWaiting > 0 {
		line += fmt.Sprintf(", %d waiting", *s.RequestsWaiting)
	}
	if s.KVCacheUsagePct != nil {
		line += fmt.Sprintf(", KV %.1f%%", *s.KVCacheUsagePct)
	}
	return line
}

func poll(ctx context.Context, cfg config.Config, mq *publisher.MQTT, sd *sdnotify.Notifier, mac string, log *slog.Logger) error {
	var vc *vllm.Client
	if !cfg.WorkerMode() {
		vc = vllm.NewClient(cfg.VLLMURL, cfg.VLLMTimeout)
	}
	var accel gpu.Reader = gpu.NvidiaSMI{Path: cfg.NvidiaSMIPath}

	tick := watchdogTick(cfg.Interval, sdnotify.WatchdogInterval())
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	var prev *vllm.Reading
	prevAt := time.Now()
	var nextPublish time.Time

	for {
		now := time.Now()

		// Published before the first tick so a restarted daemon does not
		// leave a device of unknowns for a whole interval.
		if !now.Before(nextPublish) {
			state, reading := observe(ctx, vc, accel, prev, now.Sub(prevAt), mac, log)
			if err := mq.PublishState(state); err != nil {
				log.Error("publish failed", "error", err)
			}
			if err := sd.Status("%s", statusLine(state)); err != nil {
				log.Debug("could not set status", "error", err)
			}
			prev, prevAt = &reading, now
			nextPublish = now.Add(cfg.Interval)
		}

		// Sent from the work loop, after the work: it attests that this
		// loop completed a pass, which is the only thing worth
		// attesting.
		if err := sd.Watchdog(); err != nil {
			log.Debug("watchdog ping failed", "error", err)
		}

		select {
		case <-ctx.Done():
			// Before the offline publish, so systemd counts the stop as
			// deliberate and measures TimeoutStopSec from here rather
			// than from the signal.
			if err := sd.Stopping(); err != nil {
				log.Debug("could not signal stopping", "error", err)
			}
			log.Info("shutting down")
			return nil
		case <-ticker.C:
		}
	}
}

// observe collects one round from every source. A source that fails is
// logged and left absent rather than failing the cycle: a node whose
// nvidia-smi is missing should still report its vLLM state.
func observe(
	ctx context.Context,
	vc *vllm.Client,
	accel gpu.Reader,
	prev *vllm.Reading,
	elapsed time.Duration,
	mac string,
	log *slog.Logger,
) (publisher.State, vllm.Reading) {
	var reading vllm.Reading
	state := publisher.WorkerState()
	if vc != nil {
		var err error
		if reading, err = vc.Read(ctx); err != nil {
			log.Warn("vllm read failed", "error", err)
		}
		state = publisher.FromVLLM(reading, prev, elapsed)
	}

	if g, err := accel.Read(ctx); err != nil {
		log.Warn("accelerator read failed", "adapter", accel.Name(), "error", err)
	} else {
		state.GPUUtilizationPct = g.UtilizationPct
		state.GPUClockMHz = g.ClockMHz
		state.GPUTemperatureC = g.TemperatureC
		state.GPUPowerW = g.PowerW
	}

	if m, err := host.ReadMemory(); err != nil {
		log.Warn("host memory read failed", "error", err)
	} else {
		state.MemoryAvailableBytes = m.AvailableBytes
		state.MemoryUsedPct = m.UsedPct()
	}

	// Carried rather than re-probed each cycle: it is an identity, not a
	// reading, and it is already in the retained discovery message.
	state.MACAddress = mac

	return state, reading
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
