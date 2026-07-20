//go:build darwin

package tart

import (
	"fmt"
	"os"
	"time"

	"github.com/DimmKirr/devcell/internal/ux"
)

const dhcpdLeasesPath = "/var/db/dhcpd_leases"

// WaitForGuestIP polls the macOS DHCP leases file until an IP appears for the
// given MAC address, or the timeout elapses. The MAC should be in colon-separated
// form (e.g. "aa:bb:cc:dd:ee:ff").
//
// An optional VMStateFunc checks VM liveness each iteration — if the VM enters
// "stopped" or "error" state, polling aborts immediately.
func WaitForGuestIP(mac string, timeout, interval time.Duration, stateFunc ...VMStateFunc) (string, error) {
	ux.Debugf("dhcp: waiting for guest IP (mac=%s, timeout=%s)", mac, timeout)

	var checkState VMStateFunc
	if len(stateFunc) > 0 && stateFunc[0] != nil {
		checkState = stateFunc[0]
	}

	deadline := time.Now().Add(timeout)
	attempt := 0
	for time.Now().Before(deadline) {
		attempt++

		if checkState != nil {
			state := checkState()
			ux.Debugf("dhcp: attempt %d: VM state=%s", attempt, state)
			if state == "stopped" || state == "error" {
				return "", fmt.Errorf("DHCP polling aborted: VM state is %q after %d attempts", state, attempt)
			}
		}

		data, err := os.ReadFile(dhcpdLeasesPath)
		if err != nil {
			ux.Debugf("dhcp: attempt %d: cannot read %s: %v", attempt, dhcpdLeasesPath, err)
			time.Sleep(interval)
			continue
		}
		leases := ParseDHCPLeases(string(data))
		ux.Debugf("dhcp: attempt %d: %d leases in %s", attempt, len(leases), dhcpdLeasesPath)
		if ip, ok := FindLeaseByMAC(leases, mac); ok {
			ux.Debugf("dhcp: found guest IP %s for mac %s after %d attempts", ip, mac, attempt)
			return ip, nil
		}

		// Log all MACs present in the lease file for diagnostics.
		if len(leases) > 0 {
			var macs []string
			for _, l := range leases {
				macs = append(macs, l.HWAddress+"="+l.IPAddress)
			}
			ux.Debugf("dhcp: attempt %d: no lease for mac %s; known MACs: %v", attempt, mac, macs)
		} else {
			ux.Debugf("dhcp: attempt %d: no lease for mac %s yet (lease file empty)", attempt, mac)
		}
		time.Sleep(interval)
	}
	return "", fmt.Errorf("no DHCP lease for MAC %s after %s (%d attempts)", mac, timeout, attempt)
}
