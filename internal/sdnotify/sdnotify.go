// Package sdnotify speaks systemd's service-readiness protocol.
//
// It is a few dozen lines against a documented wire format — a datagram
// of newline-separated key=value pairs to the socket named in
// NOTIFY_SOCKET — so it is written here rather than taken as a
// dependency. Everything degrades to a no-op when the environment is
// absent, which is what running outside systemd looks like.
//
// The protocol buys three things a Type=simple unit cannot have.
// Readiness means systemd holds dependent units until this one is
// actually connected to its broker rather than merely forked. Status
// means systemctl status shows what the daemon is doing right now. The
// watchdog means a daemon that wedges is killed and restarted, which a
// process that is alive but no longer polling would otherwise survive
// indefinitely.
package sdnotify

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

// Notifier sends state to systemd. The zero value is a working no-op, so
// callers never branch on whether they are running under systemd.
type Notifier struct {
	conn *net.UnixConn
}

// New connects to the notify socket. It returns a usable no-op Notifier
// when NOTIFY_SOCKET is unset or unreachable: failing to start because
// nobody was listening would be the wrong trade for a daemon that is
// perfectly able to run from a shell.
func New() *Notifier {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return &Notifier{}
	}
	// A leading @ denotes the abstract namespace, which Go spells with a
	// leading NUL.
	if addr[0] == '@' {
		addr = "\x00" + addr[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return &Notifier{}
	}
	return &Notifier{conn: conn}
}

// Enabled reports whether notifications reach systemd.
func (n *Notifier) Enabled() bool { return n != nil && n.conn != nil }

// Ready tells systemd the service is up. Under Type=notify, units
// ordered after this one do not start until it arrives, so it belongs
// after the broker connection rather than at the top of main.
func (n *Notifier) Ready() error { return n.send("READY=1") }

// Status sets the line systemctl status displays. Cheap enough to send
// every cycle, and it turns "active (running)" into something that
// answers the question an operator actually has.
func (n *Notifier) Status(format string, args ...any) error {
	return n.send("STATUS=" + fmt.Sprintf(format, args...))
}

// Watchdog sends a keepalive. Sending it from the work loop rather than
// from a timer is the entire point: a ping on its own goroutine proves
// only that the goroutine lives, which is exactly the state a wedged
// poll loop would be in.
func (n *Notifier) Watchdog() error { return n.send("WATCHDOG=1") }

// Stopping tells systemd a clean shutdown has begun, so the stop is not
// mistaken for a failure and TimeoutStopSec is measured from here.
func (n *Notifier) Stopping() error { return n.send("STOPPING=1") }

// Close releases the socket.
func (n *Notifier) Close() error {
	if !n.Enabled() {
		return nil
	}
	return n.conn.Close()
}

func (n *Notifier) send(msg string) error {
	if !n.Enabled() {
		return nil
	}
	_, err := n.conn.Write([]byte(msg))
	return err
}

// WatchdogInterval returns how often [Notifier.Watchdog] must be called,
// or zero when the watchdog is disabled.
//
// The returned interval is half of systemd's WatchdogSec, which is the
// documented convention: a daemon pinging exactly at the deadline is one
// scheduling delay away from being killed while healthy.
//
// WATCHDOG_PID is honoured because the variables are inherited across
// fork, and a child that pinged on its parent's behalf would keep a dead
// parent's service looking alive.
func WatchdogInterval() time.Duration {
	usec := os.Getenv("WATCHDOG_USEC")
	if usec == "" {
		return 0
	}
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" {
		if p, err := strconv.Atoi(pid); err != nil || p != os.Getpid() {
			return 0
		}
	}
	micros, err := strconv.ParseInt(usec, 10, 64)
	if err != nil || micros <= 0 {
		return 0
	}
	return time.Duration(micros) * time.Microsecond / 2
}
