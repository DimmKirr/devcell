package ollama_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/ollama"
)

func TestEstimateCloudSpeedTPM(t *testing.T) {
	tests := []struct {
		pricePerToken float64
		expected      float64
	}{
		{0.0000005, 18000},  // < $1/1M → very fast
		{0.000001, 9000},    // boundary: exactly $1/1M, falls to next bucket
		{0.0000009, 18000},  // just under $1/1M
		{0.000002, 9000},    // $1-5/1M
		{0.000008, 5400},    // $5-15/1M
		{0.000030, 2400},    // $15-50/1M
		{0.000100, 1200},    // > $50/1M → premium/slow
	}
	for _, tt := range tests {
		got := ollama.EstimateCloudSpeedTPM(tt.pricePerToken)
		if got != tt.expected {
			t.Errorf("EstimateCloudSpeedTPM(%v) = %v, want %v", tt.pricePerToken, got, tt.expected)
		}
	}
}

func TestEstimateLocalSpeedTPM_GenericTiers(t *testing.T) {
	// bandwidthGBs=0 → generic tier fallback (average consumer GPU).
	tests := []struct {
		paramsB  float64
		expected float64
	}{
		{1.5, 9000},  // ≤3B
		{3.0, 9000},  // ≤3B boundary
		{7.0, 4800},  // ≤8B
		{8.0, 4800},  // ≤8B boundary
		{14.0, 2700}, // ≤14B boundary
		{32.0, 1500}, // ≤32B
		{70.0, 720},  // ≤70B
		{671.0, 180}, // >70B (MoE)
		{0.0, 9000},  // unknown → fast estimate
		{-1.0, 9000}, // negative input → fast estimate
	}
	for _, tt := range tests {
		got := ollama.EstimateLocalSpeedTPM(tt.paramsB, 0)
		if got != tt.expected {
			t.Errorf("EstimateLocalSpeedTPM(%v, 0) = %v, want %v", tt.paramsB, got, tt.expected)
		}
	}
}

func TestEstimateLocalSpeedTPM_AppleSilicon(t *testing.T) {
	// M4 Pro: 273 GB/s × 0.78 efficiency. Formula: (bw*0.78) / (params*0.5625) * 60.
	const bw = 273.0
	tests := []struct {
		paramsB    float64
		wantApprox float64 // expected T/min
	}{
		{0.6, 12000}, // sub-1B: compute-bound → capped at 200 tok/s = 12000 T/m
		{8, 2839},    // (273*0.78)/(8*0.5625)*60 ≈ 2839
		{32, 710},    // (273*0.78)/(32*0.5625)*60 ≈ 710
		{70, 325},    // (273*0.78)/(70*0.5625)*60 ≈ 325
	}
	for _, tt := range tests {
		got := ollama.EstimateLocalSpeedTPM(tt.paramsB, bw)
		// Allow ±5% tolerance for floating-point rounding.
		delta := tt.wantApprox * 0.05
		if got < tt.wantApprox-delta || got > tt.wantApprox+delta {
			t.Errorf("EstimateLocalSpeedTPM(%v, %v) = %.0f, want ~%.0f (±5%%)",
				tt.paramsB, bw, got, tt.wantApprox)
		}
	}
}

func TestParseAppleSiliconBandwidth(t *testing.T) {
	tests := []struct {
		brand string
		want  float64
	}{
		{"Apple M4 Pro", 273},
		{"Apple M4 Max", 546},
		{"Apple M4", 120},
		{"Apple M3 Max", 400},
		{"Apple M3 Pro", 150},
		{"Apple M2 Max", 400},
		{"Apple M1 Ultra", 800},
		{"Intel Core i9-13900H", 0},
		{"GenuineIntel", 0},
		{"Apple M99 Pro", 0}, // unknown generation
	}
	for _, tt := range tests {
		got := ollama.ParseAppleSiliconBandwidth(tt.brand)
		if got != tt.want {
			t.Errorf("ParseAppleSiliconBandwidth(%q) = %v, want %v", tt.brand, got, tt.want)
		}
	}
}
