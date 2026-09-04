package host

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// SysClassNet and ProcNetRoute are the trees read. Overridable so the
// selection can be exercised against a recorded interface layout: the
// thing that has to be right here is which of several interfaces gets
// picked, and a developer's machine has none of the interesting ones.
var (
	SysClassNet  = "/sys/class/net"
	ProcNetRoute = "/proc/net/route"
)

// PrimaryMAC returns the ethernet hardware address identifying this
// node, lowercase and colon-separated. It returns "" and no error on a
// host with no sysfs and on one with no physical ethernet port, which
// is absence to publish nothing for rather than a failure.
//
// preferred names an interface outright and skips the selection below.
// A named interface that cannot be read is an error, because somebody
// asked for that one specifically and silently substituting another
// would defeat the point of naming it.
//
// Read out of sysfs rather than through net.Interfaces, which is not a
// style preference: net.Interfaces opens an AF_NETLINK socket, and the
// unit's RestrictAddressFamilies list is AF_INET, AF_INET6 and AF_UNIX.
// The stdlib call therefore works perfectly when run by hand and fails
// under systemd. Sysfs needs no address family at all.
func PrimaryMAC(preferred string) (string, error) {
	if preferred = strings.TrimSpace(preferred); preferred != "" {
		mac, err := readMAC(preferred)
		if err != nil {
			return "", fmt.Errorf("interface %q: %w", preferred, err)
		}
		if mac == "" {
			return "", fmt.Errorf("interface %q has no hardware address", preferred)
		}
		return mac, nil
	}

	// The interface carrying the default route wins, and it is read
	// directly rather than looked up among the filtered candidates
	// below. That distinction is the whole correctness of this function:
	// a node whose default route leaves over a bridge, a bond or a VLAN
	// has precisely the address the router sees on that interface, and
	// every one of those is filed under devices/virtual. Filtering first
	// — which this did until a reviewer pointed it out — sent exactly
	// the containerised host this is aimed at back to the fallback,
	// where it published a QSFP fabric address instead. That is the
	// failure the default-route preference exists to prevent, so the
	// filter must not run ahead of it.
	//
	// A default route over an interface with no hardware address at all
	// is common and harmless: wireguard and tun devices have none,
	// readMAC declines them, and the fallback takes over.
	if def := defaultRouteInterface(); def != "" {
		if mac, err := readMAC(def); err == nil && mac != "" {
			return mac, nil
		}
	}

	// No default route to follow, so guess: the lowest-named port that
	// looks like something a cable goes into.
	candidates, err := ethernetInterfaces()
	if err != nil || len(candidates) == 0 {
		return "", err
	}
	return candidates[0].mac, nil
}

type netInterface struct {
	name string
	mac  string
}

// ethernetInterfaces lists the physical ethernet ports carrying a usable
// address, sorted by name so the fallback choice is stable across
// restarts. An identity that changed from one boot to the next would
// move this node's Home Assistant device record with it.
//
// This is only ever the fallback. When a default route exists it names
// the right interface outright, and the guesswork here is not consulted.
func ethernetInterfaces() ([]netInterface, error) {
	entries, err := os.ReadDir(SysClassNet)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var found []netInterface
	for _, e := range entries {
		name := e.Name()
		if !isPhysicalEthernet(name) {
			continue
		}
		mac, err := readMAC(name)
		if err != nil || mac == "" {
			continue
		}
		found = append(found, netInterface{name: name, mac: mac})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].name < found[j].name })
	return found, nil
}

// isPhysicalEthernet excludes everything that has a hardware address
// without being a port somebody could plug a cable into.
//
// This narrows the guess made when there is no default route to follow,
// and it is deliberately not applied to an interface the routing table
// named. A bridge is excluded here and is still the correct answer when
// it carries the default route, because the address the router sees is
// a question about routing, not about how the kernel files the device.
//
// Four exclusions, because each catches a different thing that would
// otherwise be guessed at as this node's identity. Loopback is nobody's
// address. Anything under devices/virtual — docker0, veth pairs, unused
// bridges — is an address no router has seen, and on a node running
// containerised inference there are usually more of those than real
// ports. A type other than ARPHRD_ETHER is not an ethernet address to
// begin with. And a wireless interface is ethernet-shaped but is not
// the port an operator naming "the ethernet MAC" meant.
func isPhysicalEthernet(name string) bool {
	if name == "lo" {
		return false
	}
	dir := filepath.Join(SysClassNet, name)
	if target, err := os.Readlink(dir); err == nil && strings.Contains(target, "/virtual/") {
		return false
	}
	// ARPHRD_ETHER. Spelled as the number because that is what sysfs
	// exposes; there is no symbolic form to read here.
	if readSysAttr(dir, "type") != "1" {
		return false
	}
	for _, marker := range []string{"wireless", "phy80211"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return false
		}
	}
	return true
}

// readMAC reads one interface's address, rejecting the all-zero
// placeholder that unconfigured devices carry. Zero parses as a valid
// address and is not one, which is the same shape of bug as a metric
// that reads a convincing zero when it is missing.
func readMAC(name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(SysClassNet, name, "address"))
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(raw))
	hw, err := net.ParseMAC(text)
	if err != nil {
		return "", fmt.Errorf("address %q: %w", text, err)
	}
	for _, b := range hw {
		if b != 0 {
			// Normalised through HardwareAddr.String rather than passed
			// through as sysfs spelled it: Home Assistant matches device
			// connections on lowercase colon-separated text, and a
			// driver that spelled it otherwise would produce an address
			// that looks right and links to nothing.
			return hw.String(), nil
		}
	}
	return "", nil
}

// defaultRouteInterface names the interface carrying the IPv4 default
// route, or "" when there is none to read.
//
// /proc/net/route rather than a netlink route dump, for the same reason
// the addresses come from sysfs. Note that ProcSubset=pid would hide
// this file exactly as it hides /proc/meminfo; it is deliberately absent
// from the unit.
func defaultRouteInterface() string {
	f, err := os.Open(ProcNetRoute)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	best, bestMetric := "", math.MaxInt
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		// The header line fails the destination test and needs no
		// special case.
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		metric, err := strconv.Atoi(fields[6])
		if err != nil {
			continue
		}
		// Lowest metric wins, which is the route the kernel would
		// actually take. A node with a second uplink has more than one
		// default route and only one of them carries its traffic.
		if metric < bestMetric {
			best, bestMetric = fields[0], metric
		}
	}
	return best
}

func readSysAttr(dir, name string) string {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
