package ollama

import "syscall"

// GetSystemRAMGB returns total system RAM in GB (Linux).
func GetSystemRAMGB() float64 {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0
	}
	return float64(info.Totalram) * float64(info.Unit) / (1024 * 1024 * 1024)
}

// DetectAppleSiliconBandwidthGBs returns 0 on Linux (not Apple Silicon).
func DetectAppleSiliconBandwidthGBs() float64 { return 0 }
