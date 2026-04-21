package ollama_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/ollama"
)

func TestParseParamSize_Billions(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"32B", 32.0},
		{"70B", 70.0},
		{"8B", 8.0},
		{"7B", 7.0},
		{"1.5B", 1.5},
		{"0.6B", 0.6},
		{"235B", 235.0},
	}
	for _, tt := range tests {
		got := ollama.ParseParamSize(tt.input)
		if got != tt.expected {
			t.Errorf("ParseParamSize(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestParseParamSize_Millions(t *testing.T) {
	got := ollama.ParseParamSize("671M")
	if got != 0.671 {
		t.Errorf("ParseParamSize(671M) = %v, want 0.671", got)
	}
}

func TestParseParamSize_Empty(t *testing.T) {
	got := ollama.ParseParamSize("")
	if got != 0 {
		t.Errorf("ParseParamSize empty = %v, want 0", got)
	}
}

func TestParseParamSize_Unknown(t *testing.T) {
	got := ollama.ParseParamSize("unknown")
	if got != 0 {
		t.Errorf("ParseParamSize unknown = %v, want 0", got)
	}
}

func TestEstimateRAMGB_Q4Quantized(t *testing.T) {
	// Q4_K_M: ~0.55 bytes/param + 2 GB overhead
	tests := []struct {
		paramsB  float64
		expected float64
	}{
		{7.0, 5.85},   // 7*0.55 + 2 = 5.85
		{8.0, 6.4},    // 8*0.55 + 2 = 6.4
		{32.0, 19.6},  // 32*0.55 + 2 = 19.6
		{70.0, 40.5},  // 70*0.55 + 2 = 40.5
	}
	for _, tt := range tests {
		got := ollama.EstimateRAMGB(tt.paramsB)
		if got != tt.expected {
			t.Errorf("EstimateRAMGB(%v) = %v, want %v", tt.paramsB, got, tt.expected)
		}
	}
}

func TestEstimateRAMGB_Zero(t *testing.T) {
	got := ollama.EstimateRAMGB(0)
	if got != 0 {
		t.Errorf("EstimateRAMGB(0) = %v, want 0", got)
	}
}

func TestCheckHardware_Fits(t *testing.T) {
	ok, needed := ollama.CheckHardware("8B", 16.0)
	if !ok {
		t.Errorf("expected 8B to fit in 16GB, needed=%.1f", needed)
	}
}

func TestCheckHardware_DoesNotFit(t *testing.T) {
	ok, needed := ollama.CheckHardware("70B", 16.0)
	if ok {
		t.Errorf("expected 70B to not fit in 16GB, needed=%.1f", needed)
	}
}

func TestCheckHardware_UnknownSize(t *testing.T) {
	ok, needed := ollama.CheckHardware("", 16.0)
	if !ok {
		t.Error("expected unknown size to return ok (can't determine)")
	}
	if needed != 0 {
		t.Errorf("expected needed=0 for unknown size, got %v", needed)
	}
}

func TestGetSystemRAMGB(t *testing.T) {
	ram := ollama.GetSystemRAMGB()
	// Should return positive value on any real system
	if ram <= 0 {
		t.Errorf("expected positive system RAM, got %.1f", ram)
	}
}

func TestCheckHardwareSafe_FitsWithin75Pct(t *testing.T) {
	// 8B model needs ~6.4 GB, 75% of 16 GB = 12 GB → fits
	ok, needed := ollama.CheckHardwareSafe("8B", 16.0)
	if !ok {
		t.Errorf("expected 8B to fit safely in 16GB (needed=%.1f)", needed)
	}
}

func TestCheckHardwareSafe_DoesNotFitAt75Pct(t *testing.T) {
	// 70B model needs ~40.5 GB, 75% of 48 GB = 36 GB → does NOT fit safely
	ok, needed := ollama.CheckHardwareSafe("70B", 48.0)
	if ok {
		t.Errorf("expected 70B to not fit safely in 48GB (needed=%.1f)", needed)
	}
}

func TestCheckHardwareSafe_UnknownSize(t *testing.T) {
	ok, needed := ollama.CheckHardwareSafe("", 16.0)
	if !ok {
		t.Error("expected unknown size to return ok")
	}
	if needed != 0 {
		t.Errorf("expected needed=0 for unknown, got %v", needed)
	}
}
