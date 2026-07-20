//go:build !darwin

package tart

import (
	"fmt"
	"runtime"
	"time"
)

// WaitForGuestIP is not available on non-darwin platforms.
func WaitForGuestIP(mac string, timeout, interval time.Duration, stateFunc ...VMStateFunc) (string, error) {
	return "", fmt.Errorf("DHCP lease discovery requires darwin (current: %s)", runtime.GOOS)
}
