// Package gpu reads accelerator telemetry. The interface is small on
// purpose: everything here is optional, and a host with no accelerator
// or no vendor tooling reports absence rather than failing.
package gpu

import "context"

// Reading is one accelerator observation. Every field is optional
// because the fields available differ by hardware, and inventing a zero
// for one that is not is how a dashboard starts lying.
type Reading struct {
	UtilizationPct *float64
	ClockMHz       *float64
	TemperatureC   *float64
	// PowerW is the accelerator's own power rail, which is not the
	// module's and not the node's. See [NvidiaSMI] for what that
	// excludes on the hardware this targets.
	PowerW *float64
}

// Reader observes an accelerator.
type Reader interface {
	// Read returns what this host can report. A host with no
	// accelerator returns an empty Reading and no error: absence is a
	// state, not a failure.
	Read(ctx context.Context) (Reading, error)

	// Name identifies the adapter in logs.
	Name() string
}

// None is the adapter for a host with no accelerator telemetry, and the
// fallback when vendor tooling is missing.
type None struct{}

func (None) Read(context.Context) (Reading, error) { return Reading{}, nil }
func (None) Name() string                          { return "none" }
