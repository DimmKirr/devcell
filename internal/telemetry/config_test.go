package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestLoad_MissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg := LoadConfig(dir)
	if cfg.Enabled {
		t.Error("expected Enabled=false for missing file")
	}
	if cfg.AnonymousID != "" {
		t.Error("expected empty AnonymousID for missing file")
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	want := Config{Enabled: true, AnonymousID: "test-uuid-1234"}
	data, _ := json.Marshal(want)
	if err := os.WriteFile(filepath.Join(dir, "telemetry.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	got := LoadConfig(dir)
	if got.Enabled != want.Enabled || got.AnonymousID != want.AnonymousID {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLoad_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "telemetry.json"), []byte("{invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig(dir)
	if cfg.Enabled {
		t.Error("expected Enabled=false for corrupt file")
	}
}

func TestEnable_GeneratesUUID(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Enable(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if !uuidRe.MatchString(cfg.AnonymousID) {
		t.Errorf("AnonymousID %q is not a valid UUID v4", cfg.AnonymousID)
	}
}

func TestEnable_PreservesExistingUUID(t *testing.T) {
	dir := t.TempDir()
	cfg1, err := Enable(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg2, err := Enable(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg1.AnonymousID != cfg2.AnonymousID {
		t.Errorf("UUID changed: %q → %q", cfg1.AnonymousID, cfg2.AnonymousID)
	}
}

func TestDisable_PreservesUUID(t *testing.T) {
	dir := t.TempDir()
	cfg1, err := Enable(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg2, err := Disable(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Enabled {
		t.Error("expected Enabled=false after Disable")
	}
	if cfg2.AnonymousID != cfg1.AnonymousID {
		t.Errorf("UUID changed after Disable: %q → %q", cfg1.AnonymousID, cfg2.AnonymousID)
	}
}

func TestIsAllowed_Enabled(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	cfg := Config{Enabled: true, AnonymousID: "test"}
	if !IsAllowed(cfg) {
		t.Error("expected IsAllowed=true when Enabled and DO_NOT_TRACK unset")
	}
}

func TestIsAllowed_Disabled(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	cfg := Config{Enabled: false}
	if IsAllowed(cfg) {
		t.Error("expected IsAllowed=false when Enabled=false")
	}
}

func TestIsAllowed_DoNotTrack(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "1")
	cfg := Config{Enabled: true, AnonymousID: "test"}
	if IsAllowed(cfg) {
		t.Error("expected IsAllowed=false when DO_NOT_TRACK=1")
	}
}
