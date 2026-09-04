// Command sparkrustler publishes the state of one vLLM node to Home
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

	"github.com/nugget/sparkrustler/internal/config"
	"github.com/nugget/sparkrustler/internal/gpu"
	"github.com/nugget/sparkrustler/internal/hadiscovery"
	"github.com/nugget/sparkrustler/internal/host"
	"github.com/nugget/sparkrustler/internal/publisher"
	"github.com/nugget/sparkrustler/internal/vllm"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "sparkrustler:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	publisher.Device = hadiscovery.Device{
		Name:         firstNonEmpty(cfg.NodeName, cfg.NodeID),
		Manufacturer: "NVIDIA",
		Model:        cfg.DeviceModel,
		SWVersion:    hadiscovery.Version,
		ConfigURL:    cfg.VLLMURL,
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
		Logger:          log,
	})
	if err != nil {
		return err
	}
	defer mq.Close()

	// SIGTERM as well as SIGINT: systemd sends the former, and without
	// it a stopped unit would skip the offline publish and leave its
	// device showing online until the will eventually fired.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("publishing",
		"node", cfg.NodeID,
		"vllm", cfg.VLLMURL,
		"broker", cfg.BrokerURL,
		"interval", cfg.Interval,
	)
	return poll(ctx, cfg, mq, log)
}

func poll(ctx context.Context, cfg config.Config, mq *publisher.MQTT, log *slog.Logger) error {
	vc := vllm.NewClient(cfg.VLLMURL, cfg.VLLMTimeout)
	var accel gpu.Reader = gpu.NvidiaSMI{Path: cfg.NvidiaSMIPath}

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	var prev *vllm.Reading
	prevAt := time.Now()

	for {
		// Published before the first tick so a restarted daemon does not
		// leave a device of unknowns for a whole interval.
		now := time.Now()
		state, reading := observe(ctx, vc, accel, prev, now.Sub(prevAt), log)
		if err := mq.PublishState(state); err != nil {
			log.Error("publish failed", "error", err)
		}
		prev, prevAt = &reading, now

		select {
		case <-ctx.Done():
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
	log *slog.Logger,
) (publisher.State, vllm.Reading) {
	reading, err := vc.Read(ctx)
	if err != nil {
		log.Warn("vllm read failed", "error", err)
	}
	state := publisher.FromVLLM(reading, prev, elapsed)

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
	}

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
