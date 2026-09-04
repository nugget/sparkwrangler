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
