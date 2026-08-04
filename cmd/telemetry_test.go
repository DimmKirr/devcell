package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/telemetry"
)

func TestTelemetryOn_CreatesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DO_NOT_TRACK", "")

	configDir := filepath.Join(dir, "devcell")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	out := new(strings.Builder)
	cmd := telemetryOnCmd
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(configDir, "telemetry.json"))
	if err != nil {
		t.Fatalf("telemetry.json not created: %v", err)
	}
	var cfg telemetry.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if cfg.AnonymousID == "" {
		t.Error("expected non-empty AnonymousID")
	}
}

func TestTelemetryOff_DisablesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DO_NOT_TRACK", "")

	configDir := filepath.Join(dir, "devcell")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Enable first
	cfg, err := telemetry.Enable(configDir)
	if err != nil {
		t.Fatal(err)
	}
	origID := cfg.AnonymousID

	out := new(strings.Builder)
	cmd := telemetryOffCmd
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}

	cfg = telemetry.LoadConfig(configDir)
	if cfg.Enabled {
		t.Error("expected Enabled=false after off")
	}
	if cfg.AnonymousID != origID {
		t.Errorf("UUID changed: %q → %q", origID, cfg.AnonymousID)
	}
}

func TestTelemetryStatus_ShowsState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DO_NOT_TRACK", "")

	configDir := filepath.Join(dir, "devcell")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := telemetry.Enable(configDir); err != nil {
		t.Fatal(err)
	}

	out := new(strings.Builder)
	cmd := telemetryStatusCmd
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "enabled") {
		t.Errorf("output %q does not contain 'enabled'", out.String())
	}
}

func TestTelemetryStatus_ShowsDoNotTrack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DO_NOT_TRACK", "1")

	configDir := filepath.Join(dir, "devcell")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	out := new(strings.Builder)
	cmd := telemetryStatusCmd
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "DO_NOT_TRACK") {
		t.Errorf("output %q does not mention DO_NOT_TRACK", out.String())
	}
}
