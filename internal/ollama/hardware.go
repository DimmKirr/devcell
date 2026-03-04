package ollama

import (
	"math"
	"strconv"
	"strings"
)

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

