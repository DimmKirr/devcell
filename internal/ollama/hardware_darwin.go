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

// DetectAppleSiliconBandwidthGBs returns the memory bandwidth in GB/s for the
// current Apple Silicon chip, or 0 if unrecognised.
// Reads "machdep.cpu.brand_string" via sysctl.
func DetectAppleSiliconBandwidthGBs() float64 {
	out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
	if err != nil {
		return 0
	}
	return ParseAppleSiliconBandwidth(strings.TrimSpace(string(out)))
}
