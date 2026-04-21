package ollama

import (
	"math"
	"strconv"
	"strings"
)

// appleSiliconBandwidth maps (generation, tier) → memory bandwidth in GB/s.
// Sources: Apple spec sheets and llama.cpp community benchmarks.
var appleSiliconBandwidth = map[int]map[string]float64{
	1: {"": 68, "pro": 200, "max": 400, "ultra": 800},
	2: {"": 100, "pro": 200, "max": 400, "ultra": 800},
	3: {"": 100, "pro": 150, "max": 400, "ultra": 800},
	4: {"": 120, "pro": 273, "max": 546, "ultra": 1092},
}

// ParseAppleSiliconBandwidth parses a CPU brand string like "Apple M4 Pro"
// and returns the corresponding memory bandwidth in GB/s, or 0 if unrecognised.
func ParseAppleSiliconBandwidth(brandString string) float64 {
	const prefix = "Apple M"
	if !strings.HasPrefix(brandString, prefix) {
		return 0
	}
	rest := brandString[len(prefix):]

	// Extract generation digits.
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	gen, err := strconv.Atoi(rest[:i])
	if err != nil {
		return 0
	}

	// Tier: "Pro", "Max", "Ultra", or "" (base) — normalised to lowercase.
	tier := strings.ToLower(strings.TrimSpace(rest[i:]))

	tiers, ok := appleSiliconBandwidth[gen]
	if !ok {
		return 0
	}
	return tiers[tier] // 0 if tier unknown
}

// ParseParamSize parses a parameter size string like "32B" or "671M" into
// billions of parameters. Returns 0 if unparseable.
func ParseParamSize(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	upper := strings.ToUpper(s)
	multiplier := 1.0

	if strings.HasSuffix(upper, "B") {
		s = s[:len(s)-1]
		multiplier = 1.0
	} else if strings.HasSuffix(upper, "M") {
		s = s[:len(s)-1]
		multiplier = 0.001
	} else {
		return 0
	}

	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}

	return val * multiplier
}

// EstimateRAMGB estimates RAM needed (GB) for a Q4_K_M quantized model.
// Formula: params_in_billions * 0.55 + 2 GB overhead.
// Returns 0 if paramsB is 0.
func EstimateRAMGB(paramsB float64) float64 {
	if paramsB == 0 {
		return 0
	}
	return math.Round((paramsB*0.55+2)*100) / 100
}

// CheckHardware checks if a model with given parameter size fits in available RAM.
// Returns (ok, neededGB). If parameter size is unknown, returns (true, 0).
func CheckHardware(parameterSize string, systemRAMGB float64) (bool, float64) {
	paramsB := ParseParamSize(parameterSize)
	if paramsB == 0 {
		return true, 0
	}
	needed := EstimateRAMGB(paramsB)
	return needed <= systemRAMGB, needed
}

// CheckHardwareSafe checks if a model fits within 75% of available RAM.
// Uses a conservative threshold: a 48 GB model won't run on 48 GB RAM.
// Returns (ok, neededGB). If parameter size is unknown, returns (true, 0).
func CheckHardwareSafe(parameterSize string, systemRAMGB float64) (bool, float64) {
	paramsB := ParseParamSize(parameterSize)
	if paramsB == 0 {
		return true, 0
	}
	needed := EstimateRAMGB(paramsB)
	return needed <= systemRAMGB*0.75, needed
}

