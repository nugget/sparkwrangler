package host

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeIface is one entry in a recorded /sys/class/net.
type fakeIface struct {
	name string
	mac  string
	// kind is the sysfs "type" attribute. Empty means 1, ARPHRD_ETHER.
	kind string
	// virtual places the device under devices/virtual, which is where
	// the kernel files docker bridges, veth pairs and bonds.
	virtual  bool
	wireless bool
}

// writeSysfs builds a tree shaped like the real /sys/class/net,
// symlinks and all. Symlinks rather than plain directories because the
// virtual-device test reads the link target, which is the only thing
// distinguishing docker0 from a network port.
func writeSysfs(t *testing.T, ifaces []fakeIface) {
	t.Helper()

	root := t.TempDir()
	classNet := filepath.Join(root, "class", "net")
	if err := os.MkdirAll(classNet, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for _, i := range ifaces {
		bus := "platform"
		if i.virtual {
			bus = "virtual"
		}
		dir := filepath.Join(root, "devices", bus, "net", i.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		write := func(name, content string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		write("address", i.mac)
		if i.kind == "" {
			i.kind = "1"
		}
		write("type", i.kind)
		if i.wireless {
			if err := os.MkdirAll(filepath.Join(dir, "wireless"), 0o755); err != nil {
				t.Fatalf("mkdir wireless: %v", err)
			}
		}
		target := filepath.Join("..", "..", "devices", bus, "net", i.name)
		if err := os.Symlink(target, filepath.Join(classNet, i.name)); err != nil {
			t.Fatalf("symlink: %v", err)
		}
	}

	original := SysClassNet
	t.Cleanup(func() { SysClassNet = original })
	SysClassNet = classNet
}

// writeRoute records a /proc/net/route. Absent lines mean a host with
// no default route, which is a real state on a fabric-only worker.
func writeRoute(t *testing.T, content string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "route")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write route: %v", err)
	}
	original := ProcNetRoute
	t.Cleanup(func() { ProcNetRoute = original })
	ProcNetRoute = path
}

const routeHeader = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"

// TestPrimaryMACSelection pins which port's address a node is published
// under. Getting this wrong is silent in the worst way: the daemon comes
// up, the sensor shows a plausible address, and the device link to
// whatever else Home Assistant knows about this machine simply never
// forms, with nothing logged anywhere to say why.
func TestPrimaryMACSelection(t *testing.T) {
	tests := []struct {
		name      string
		ifaces    []fakeIface
		route     string
		preferred string
		want      string
		wantErr   bool
	}{
		{
			name: "the default route wins over the name that sorts first",
			// The shape of a Spark: two QSFP fabric ports whose kernel
			// names sort ahead of the RJ45 the house network sees. Taking
			// the first would publish an address no router has heard of.
			ifaces: []fakeIface{
				{name: "enp1s0f0np0", mac: "aa:bb:cc:00:00:01"},
				{name: "enp1s0f1np1", mac: "aa:bb:cc:00:00:02"},
				{name: "enp2s0", mac: "aa:bb:cc:00:00:03"},
			},
			route: routeHeader +
				"enp2s0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n",
			want: "aa:bb:cc:00:00:03",
		},
		{
			name: "the lowest metric wins when two default routes exist",
			ifaces: []fakeIface{
				{name: "enp1s0", mac: "aa:bb:cc:00:00:01"},
				{name: "enp2s0", mac: "aa:bb:cc:00:00:02"},
			},
			route: routeHeader +
				"enp1s0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
				"enp2s0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n",
			want: "aa:bb:cc:00:00:02",
		},
		{
			name: "no default route falls back to the first port by name",
			ifaces: []fakeIface{
				{name: "enp2s0", mac: "aa:bb:cc:00:00:02"},
				{name: "enp1s0", mac: "aa:bb:cc:00:00:01"},
			},
			route: routeHeader,
			want:  "aa:bb:cc:00:00:01",
		},
		{
			name: "a default route over an interface with no address is ignored",
			ifaces: []fakeIface{
				{name: "enp1s0", mac: "aa:bb:cc:00:00:01"},
			},
			route: routeHeader +
				"tailscale0\t00000000\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n",
			want: "aa:bb:cc:00:00:01",
		},
		{
			name: "virtual devices are not this node's identity",
			// A node running containerised inference has more of these
			// than real ports, and docker0 sorts ahead of every en*.
			ifaces: []fakeIface{
				{name: "docker0", mac: "02:42:9a:00:00:01", virtual: true},
				{name: "veth8a1b2c3", mac: "7a:11:22:33:44:55", virtual: true},
				{name: "br0", mac: "02:42:9a:00:00:02", virtual: true},
				{name: "enp1s0", mac: "aa:bb:cc:00:00:01"},
			},
			route: routeHeader,
			want:  "aa:bb:cc:00:00:01",
		},
		{
			name: "loopback is not an identity",
			ifaces: []fakeIface{
				{name: "lo", mac: "00:00:00:00:00:00", kind: "772"},
				{name: "enp1s0", mac: "aa:bb:cc:00:00:01"},
			},
			route: routeHeader,
			want:  "aa:bb:cc:00:00:01",
		},
		{
			name: "wireless is ethernet-shaped and is not the ethernet port",
			ifaces: []fakeIface{
				{name: "enp1s0", mac: "aa:bb:cc:00:00:01"},
				{name: "wlp3s0", mac: "dd:ee:ff:00:00:01", wireless: true},
			},
			route: routeHeader,
			want:  "aa:bb:cc:00:00:01",
		},
		{
			name: "an all-zero address parses and is not an address",
			ifaces: []fakeIface{
				{name: "enp1s0", mac: "00:00:00:00:00:00"},
				{name: "enp2s0", mac: "aa:bb:cc:00:00:02"},
			},
			route: routeHeader,
			want:  "aa:bb:cc:00:00:02",
		},
		{
			name: "an infiniband port is not an ethernet port",
			ifaces: []fakeIface{
				{name: "ibp1s0", mac: "aa:bb:cc:00:00:01", kind: "32"},
				{name: "enp2s0", mac: "aa:bb:cc:00:00:02"},
			},
			route: routeHeader,
			want:  "aa:bb:cc:00:00:02",
		},
		{
			name: "a named interface beats the selection, virtual or not",
			ifaces: []fakeIface{
				{name: "br0", mac: "02:42:9a:00:00:02", virtual: true},
				{name: "enp1s0", mac: "aa:bb:cc:00:00:01"},
			},
			route:     routeHeader,
			preferred: "br0",
			want:      "02:42:9a:00:00:02",
		},
		{
			name: "a named interface that is not there is an error, not a substitution",
			ifaces: []fakeIface{
				{name: "enp1s0", mac: "aa:bb:cc:00:00:01"},
			},
			route:     routeHeader,
			preferred: "enp9s0",
			wantErr:   true,
		},
		{
			name:   "a host with no ethernet port publishes nothing",
			ifaces: []fakeIface{{name: "lo", mac: "00:00:00:00:00:00", kind: "772"}},
			route:  routeHeader,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeSysfs(t, tt.ifaces)
			writeRoute(t, tt.route)

			got, err := PrimaryMAC(tt.preferred)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("PrimaryMAC = %q, want an error naming the interface", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PrimaryMAC: %v", err)
			}
			if got != tt.want {
				t.Errorf("PrimaryMAC = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPrimaryMACOnAPlatformWithoutSysfs pins that a developer's machine
// reports absence rather than an error, the same way a missing
// /proc/meminfo does. This daemon is written on hosts with no sysfs.
func TestPrimaryMACOnAPlatformWithoutSysfs(t *testing.T) {
	originalSys, originalRoute := SysClassNet, ProcNetRoute
	t.Cleanup(func() { SysClassNet, ProcNetRoute = originalSys, originalRoute })
	SysClassNet, ProcNetRoute = "/nonexistent/class/net", "/nonexistent/route"

	mac, err := PrimaryMAC("")
	if err != nil {
		t.Fatalf("PrimaryMAC: %v, want absence without error", err)
	}
	if mac != "" {
		t.Errorf("PrimaryMAC = %q, want nothing on a host with no sysfs", mac)
	}
}

// TestMACIsNormalised pins the form published. Home Assistant matches
// device connections on lowercase colon-separated text, so an address a
// driver spelled in upper case would look right in the sensor and link
// to nothing.
func TestMACIsNormalised(t *testing.T) {
	writeSysfs(t, []fakeIface{{name: "enp1s0", mac: "AA-BB-CC-00-00-01"}})
	writeRoute(t, routeHeader)

	got, err := PrimaryMAC("")
	if err != nil {
		t.Fatalf("PrimaryMAC: %v", err)
	}
	if want := "aa:bb:cc:00:00:01"; got != want {
		t.Errorf("PrimaryMAC = %q, want %q", got, want)
	}
}
