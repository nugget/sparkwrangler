package gpu

import "testing"

// TestParseSMI pins that an unsupported field does not discard the
// supported ones. On GB10 this is the normal case, not an edge case:
// anything backed by a discrete framebuffer answers [N/A] while
// utilisation, clock and temperature are all perfectly good.
func TestParseSMI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                          string
		out                           string
		wantUtil, wantClock, wantTemp *float64
		wantPower                     *float64
	}{
		{
			name:     "a fully supported row",
			out:      "35, 2177, 43, 91.34\n",
			wantUtil: f(35), wantClock: f(2177), wantTemp: f(43), wantPower: f(91.34),
		},
		{
			name:     "an unsupported field leaves the others intact",
			out:      "95, 2184, 47, [N/A]\n",
			wantUtil: f(95), wantClock: f(2184), wantTemp: f(47),
		},
		{
			name:     "the older not-supported spelling",
			out:      "12, 1200, 40, [Not Supported]\n",
			wantUtil: f(12), wantClock: f(1200), wantTemp: f(40),
		},
		{
			name: "no output at all",
			out:  "\n",
		},
		{
			name: "a truncated row is not half-read",
			out:  "35, 2177\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSMI(tt.out)
			check(t, "utilization", got.UtilizationPct, tt.wantUtil)
			check(t, "clock", got.ClockMHz, tt.wantClock)
			check(t, "temperature", got.TemperatureC, tt.wantTemp)
			check(t, "power", got.PowerW, tt.wantPower)
		})
	}
}

func f(v float64) *float64 { return &v }

func check(t *testing.T, name string, got, want *float64) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %v, want absent", name, *got)
	case want != nil && got == nil:
		t.Errorf("%s = absent, want %v", name, *want)
	case want != nil && *got != *want:
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}
