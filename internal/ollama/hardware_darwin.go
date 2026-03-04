package ollama

import (
	"os/exec"
	"strconv"
	"strings"
)

// GetSystemRAMGB returns total system RAM in GB (macOS).
func GetSystemRAMGB() float64 {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	bytes, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return float64(bytes) / (1024 * 1024 * 1024)
}
