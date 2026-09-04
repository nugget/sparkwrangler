package host

import (
	"strings"
	"testing"
)

// Recorded from a DGX Spark, trimmed to the lines this reads plus the
// MemFree that must not be mistaken for MemAvailable.
const sparkMemInfo = `MemTotal:       125501816 kB
MemFree:         8123456 kB
MemAvailable:   114204672 kB
Buffers:          204800 kB
Cached:         98765432 kB
`

func TestParseMemInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		in            string
		wantTotal     int64
		wantAvailable int64
		wantAbsent    bool
	}{
		{
			name: "a real meminfo",
			in:   sparkMemInfo,
			// kB in meminfo means kibibytes, so the conversion is 1024.
			wantTotal:     125501816 * 1024,
			wantAvailable: 114204672 * 1024,
		},
		{
			name:       "a kernel too old for MemAvailable",
			in:         "MemTotal:  1024 kB\nMemFree:  512 kB\n",
			wantTotal:  1024 * 1024,
			wantAbsent: true,
		},
		{
			name:       "empty",
			in:         "",
			wantAbsent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parseMemInfo(strings.NewReader(tt.in))
			if err != nil {
				t.Fatalf("parseMemInfo: %v", err)
			}
			if tt.wantAbsent {
				if m.AvailableBytes != nil {
					t.Errorf("AvailableBytes = %d, want absent", *m.AvailableBytes)
				}
			} else {
				if m.AvailableBytes == nil || *m.AvailableBytes != tt.wantAvailable {
					t.Errorf("AvailableBytes = %v, want %d", m.AvailableBytes, tt.wantAvailable)
				}
			}
			if tt.wantTotal > 0 {
				if m.TotalBytes == nil || *m.TotalBytes != tt.wantTotal {
					t.Errorf("TotalBytes = %v, want %d", m.TotalBytes, tt.wantTotal)
				}
			}
		})
	}
}

// TestReadMemoryOnAPlatformWithoutProc pins that a missing /proc/meminfo
// is absence rather than an error. This daemon is developed on hosts
// that do not have one.
func TestReadMemoryOnAPlatformWithoutProc(t *testing.T) {
	original := MemInfoPath
	t.Cleanup(func() { MemInfoPath = original })
	MemInfoPath = "/nonexistent/meminfo"

	m, err := ReadMemory()
	if err != nil {
		t.Fatalf("ReadMemory: %v, want absence without error", err)
	}
	if m.AvailableBytes != nil || m.TotalBytes != nil {
		t.Error("reported memory on a platform with no meminfo")
	}
}

// TestUsedPct pins the gauge reading against the byte reading it is
// derived from, and pins that an incomplete meminfo produces no
// percentage at all. A percentage is the reading an operator will put a
// red band on, and one computed from a total nobody read would be a
// confident number with nothing behind it.
func TestUsedPct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		in         string
		want       float64
		wantAbsent bool
	}{
		{
			// 125501816 total, 114204672 available: the node is calm, and
			// the number a gauge should show is the ~9% that is spoken
			// for, not the 93.5% that MemFree alone would imply.
			name: "a real meminfo",
			in:   sparkMemInfo,
			want: 9.001,
		},
		{
			name:       "a kernel too old for MemAvailable",
			in:         "MemTotal:  1024 kB\nMemFree:  512 kB\n",
			wantAbsent: true,
		},
		{
			name:       "a total of zero is not a denominator",
			in:         "MemTotal:  0 kB\nMemAvailable:  0 kB\n",
			wantAbsent: true,
		},
		{
			name:       "empty",
			in:         "",
			wantAbsent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parseMemInfo(strings.NewReader(tt.in))
			if err != nil {
				t.Fatalf("parseMemInfo: %v", err)
			}
			got := m.UsedPct()
			if tt.wantAbsent {
				if got != nil {
					t.Errorf("UsedPct = %v, want absent", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("UsedPct = absent, want %v", tt.want)
			}
			if diff := *got - tt.want; diff > 0.001 || diff < -0.001 {
				t.Errorf("UsedPct = %v, want %v", *got, tt.want)
			}
		})
	}
}

// TestUsedPctAgreesWithAvailableBytes pins that the gauge and the byte
// sensor beside it are two renderings of one reading rather than two
// readings. They are published together and an operator will read them
// together; a discrepancy between them would be blamed on the node.
func TestUsedPctAgreesWithAvailableBytes(t *testing.T) {
	t.Parallel()

	m, err := parseMemInfo(strings.NewReader(sparkMemInfo))
	if err != nil {
		t.Fatalf("parseMemInfo: %v", err)
	}
	pct := m.UsedPct()
	if pct == nil {
		t.Fatal("UsedPct = absent")
	}

	implied := float64(*m.TotalBytes) * (1 - *pct/100)
	if diff := implied - float64(*m.AvailableBytes); diff > 1 || diff < -1 {
		t.Errorf("the percentage implies %.0f available bytes, but %d is published",
			implied, *m.AvailableBytes)
	}
}
