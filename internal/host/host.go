// Package host reads the memory the accelerator actually competes for.
//
// On unified-memory hardware there is no separate framebuffer to query:
// model weights, KV cache and the page cache all draw on one pool, so
// the host's own accounting is the only view of how close a node is to
// the wall. This is the number that falls before a box stops answering.
package host

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Memory is the host's memory accounting, in bytes. Absent when this
// platform does not publish it, which is every non-Linux host.
type Memory struct {
	TotalBytes     *int64
	AvailableBytes *int64
}

// MemInfoPath is the file read. Overridable so the parser can be tested
// against recorded fixtures from the hosts this targets.
var MemInfoPath = "/proc/meminfo"

// ReadMemory returns the host's memory accounting. A platform without
// /proc/meminfo reports absence rather than an error: this daemon is
// developed on machines that do not have it and runs on ones that do.
func ReadMemory() (Memory, error) {
	f, err := os.Open(MemInfoPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Memory{}, nil
		}
		return Memory{}, err
	}
	defer func() { _ = f.Close() }()
	return parseMemInfo(f)
}

// parseMemInfo reads MemTotal and MemAvailable.
//
// MemAvailable rather than MemFree, and the difference is the whole
// point: MemFree excludes reclaimable page cache, so after a large model
// download it reads alarmingly low on a host that is fine. MemAvailable
// is the kernel's own estimate of what a new allocation could get.
//
// Note that it is still not what the accelerator's allocator sees, which
// on unified memory can be substantially lower — page cache that the
// kernel counts as reclaimable is not necessarily available to a CUDA
// context at the instant it asks. Treat this as the leading alarm, not
// as the go/no-go for a launch.
func parseMemInfo(r interface {
	Read([]byte) (int, error)
}) (Memory, error) {
	var m Memory

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		switch key {
		case "MemTotal":
			m.TotalBytes = kibToBytes(value)
		case "MemAvailable":
			m.AvailableBytes = kibToBytes(value)
		}
		if m.TotalBytes != nil && m.AvailableBytes != nil {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return Memory{}, err
	}
	return m, nil
}

// kibToBytes converts a meminfo value, which is in kibibytes despite
// being spelled kB.
func kibToBytes(value string) *int64 {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil
	}
	kib, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil
	}
	bytes := kib * 1024
	return &bytes
}
