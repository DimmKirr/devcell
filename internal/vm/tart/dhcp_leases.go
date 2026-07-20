package tart

import (
	"strings"
)

// DHCPLease represents a single entry from macOS /var/db/dhcpd_leases.
type DHCPLease struct {
	Name      string
	IPAddress string
	HWAddress string // MAC address without type prefix (e.g. "aa:bb:cc:dd:ee:ff")
}

// ParseDHCPLeases parses the macOS DHCP lease file format.
// Each entry is enclosed in { } and contains key=value pairs.
// hw_address may have a type prefix (e.g. "1,aa:bb:cc:dd:ee:ff") which is stripped.
func ParseDHCPLeases(content string) []DHCPLease {
	var leases []DHCPLease
	var current *DHCPLease

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "{" {
			current = &DHCPLease{}
			continue
		}
		if line == "}" {
			if current != nil && current.IPAddress != "" {
				leases = append(leases, *current)
			}
			current = nil
			continue
		}
		if current == nil {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		switch key {
		case "name":
			current.Name = val
		case "ip_address":
			current.IPAddress = val
		case "hw_address":
			if idx := strings.Index(val, ","); idx >= 0 {
				val = val[idx+1:]
			}
			current.HWAddress = strings.ToLower(val)
		}
	}
	return leases
}

// FindLeaseByMAC returns the IP address for the given MAC address.
// Comparison is case-insensitive.
func FindLeaseByMAC(leases []DHCPLease, mac string) (string, bool) {
	mac = strings.ToLower(mac)
	for _, l := range leases {
		if l.HWAddress == mac {
			return l.IPAddress, true
		}
	}
	return "", false
}
