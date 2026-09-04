package sdnotify

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestNotifierSendsToSocket runs the real protocol against a real
// unixgram socket, because the value of this package is entirely in
// whether systemd receives the bytes.
func TestNotifierSendsToSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "notify.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Skipf("unixgram unavailable: %v", err)
	}
	defer func() { _ = conn.Close() }()

	t.Setenv("NOTIFY_SOCKET", sock)
	n := New()
	defer func() { _ = n.Close() }()

	if !n.Enabled() {
		t.Fatal("Enabled() = false with NOTIFY_SOCKET set")
	}

	tests := []struct {
		name string
		send func() error
		want string
	}{
		{name: "ready", send: n.Ready, want: "READY=1"},
		{name: "watchdog", send: n.Watchdog, want: "WATCHDOG=1"},
		{name: "stopping", send: n.Stopping, want: "STOPPING=1"},
		{
			name: "status",
			send: func() error { return n.Status("serving %s, %d running", "qwen", 3) },
			want: "STATUS=serving qwen, 3 running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.send(); err != nil {
				t.Fatalf("send: %v", err)
			}
			buf := make([]byte, 256)
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			read, _, err := conn.ReadFrom(buf)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got := string(buf[:read]); got != tt.want {
				t.Errorf("received %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNotifierWithoutSystemdIsANoOp pins that every call is safe when
// nothing is listening. The daemon runs from a shell during development
// and must not branch on that.
func TestNotifierWithoutSystemdIsANoOp(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	n := New()
	if n.Enabled() {
		t.Fatal("Enabled() = true with no NOTIFY_SOCKET")
	}
	for name, call := range map[string]func() error{
		"Ready": n.Ready, "Watchdog": n.Watchdog, "Stopping": n.Stopping, "Close": n.Close,
	} {
		if err := call(); err != nil {
			t.Errorf("%s() = %v, want nil when not under systemd", name, err)
		}
	}
	if err := n.Status("anything"); err != nil {
		t.Errorf("Status() = %v, want nil", err)
	}
}

// TestNotifierWithUnreachableSocketIsANoOp pins that a stale
// NOTIFY_SOCKET does not stop the daemon starting.
func TestNotifierWithUnreachableSocketIsANoOp(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "absent.sock"))
	if New().Enabled() {
		t.Error("Enabled() = true for a socket nothing is listening on")
	}
}

func TestWatchdogInterval(t *testing.T) {
	tests := []struct {
		name string
		usec string
		pid  string
		want time.Duration
	}{
		{
			// Half of WatchdogSec, per systemd's own guidance: pinging
			// at the deadline is one scheduling delay from being killed
			// while healthy.
			name: "half the configured deadline",
			usec: "30000000",
			want: 15 * time.Second,
		},
		{name: "unset disables the watchdog", want: 0},
		{name: "a malformed value disables it", usec: "soon", want: 0},
		{name: "a non-positive value disables it", usec: "0", want: 0},
		{
			// The variables survive fork; a child pinging on a dead
			// parent's behalf would keep a broken service looking alive.
			name: "another process's watchdog is not ours",
			usec: "30000000",
			pid:  "1",
			want: 0,
		},
		{name: "our own pid is honoured", usec: "10000000", pid: strconv.Itoa(os.Getpid()), want: 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WATCHDOG_USEC", tt.usec)
			t.Setenv("WATCHDOG_PID", tt.pid)
			if got := WatchdogInterval(); got != tt.want {
				t.Errorf("WatchdogInterval() = %s, want %s", got, tt.want)
			}
		})
	}
}
