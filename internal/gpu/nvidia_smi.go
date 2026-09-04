package gpu

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// NvidiaSMI reads an NVIDIA accelerator through the nvidia-smi CLI.
//
// The CLI rather than NVML bindings, deliberately: NVML requires cgo and
// a matching driver library at build time, which would make this awkward
// to cross-compile for the very hosts it targets. nvidia-smi is present
// wherever the driver is.
//
// Memory is not queried here. On unified-memory parts such as GB10 there
// is no discrete framebuffer, NVML answers ERROR_NOT_SUPPORTED, and
// nvidia-smi reports [N/A]; the meaningful number is host memory, which
// the host package reads. Asking here would produce a field that is
// always absent on exactly the hardware this was written for.
type NvidiaSMI struct {
	// Path overrides the executable. Empty resolves nvidia-smi on PATH.
	Path string
}

func (n NvidiaSMI) Name() string { return "nvidia-smi" }

// query is fixed rather than configurable so the field order and the
// parsing below cannot drift apart.
const smiQuery = "utilization.gpu,clocks.sm,temperature.gpu,power.draw"

func (n NvidiaSMI) Read(ctx context.Context) (Reading, error) {
	bin := n.Path
	if bin == "" {
		bin = "nvidia-smi"
	}
	// A missing binary is a host without the driver, which is absence
	// rather than an error worth propagating to a poll loop that would
	// only log it every interval forever.
	if _, err := exec.LookPath(bin); err != nil {
		return Reading{}, nil
	}

	out, err := exec.CommandContext(ctx, bin,
		"--query-gpu="+smiQuery,
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return Reading{}, err
	}
	return parseSMI(string(out)), nil
}

// parseSMI reads the first GPU's row. Fields are parsed independently so
// that one unsupported value does not discard the rest — which is the
// normal case on GB10, where some fields answer [N/A] and the others are
// perfectly good.
func parseSMI(out string) Reading {
	var r Reading

	line := firstNonEmptyLine(out)
	if line == "" {
		return r
	}
	fields := strings.Split(line, ",")
	if len(fields) < 4 {
		return r
	}

	r.UtilizationPct = smiFloat(fields[0])
	r.ClockMHz = smiFloat(fields[1])
	r.TemperatureC = smiFloat(fields[2])
	r.PowerW = smiFloat(fields[3])
	return r
}

func firstNonEmptyLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// smiFloat parses one field, treating nvidia-smi's several spellings of
// "no value" as absent. [N/A] is what GB10 returns for anything backed
// by a discrete framebuffer, and [Not Supported] appears on consumer
// parts for power on some driver versions.
func smiFloat(field string) *float64 {
	v := strings.TrimSpace(field)
	if v == "" || strings.HasPrefix(v, "[") {
		return nil
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	return &parsed
}
