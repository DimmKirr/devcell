package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/posthog/posthog-go"
)

func enabledDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{Enabled: true, AnonymousID: "test-user-123"}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "telemetry.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func disabledDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{Enabled: false, AnonymousID: "test-user-123"}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "telemetry.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInit_DisabledConfig(t *testing.T) {
	dir := disabledDir(t)
	t.Setenv("DO_NOT_TRACK", "")
	Init(dir)
	defer Close()
	if client != nil {
		t.Error("expected nil client when telemetry disabled")
	}
}

func TestInit_DoNotTrack(t *testing.T) {
	dir := enabledDir(t)
	t.Setenv("DO_NOT_TRACK", "1")
	Init(dir)
	defer Close()
	if client != nil {
		t.Error("expected nil client when DO_NOT_TRACK=1")
	}
}

func TestInit_Enabled(t *testing.T) {
	dir := enabledDir(t)
	t.Setenv("DO_NOT_TRACK", "")
	Init(dir)
	defer Close()
	if client == nil {
		t.Error("expected non-nil client when telemetry enabled")
	}
}

func TestTrackCommandRun_Properties(t *testing.T) {
	dir := enabledDir(t)
	t.Setenv("DO_NOT_TRACK", "")

	var captured []posthog.Capture
	captureHook = func(c posthog.Capture) { captured = append(captured, c) }
	defer func() { captureHook = nil }()

	Init(dir)
	defer Close()

	TrackCommandRun("claude", "docker", "go", []string{"llm", "node"}, true)

	if len(captured) != 1 {
		t.Fatalf("expected 1 capture, got %d", len(captured))
	}
	c := captured[0]
	if c.Event != "command_run" {
		t.Errorf("event = %q, want command_run", c.Event)
	}
	if c.DistinctId != "test-user-123" {
		t.Errorf("distinctId = %q, want test-user-123", c.DistinctId)
	}
	checks := map[string]any{
		"command": "claude",
		"engine":  "docker",
		"stack":   "go",
		"thin":    true,
	}
	for k, want := range checks {
		got := c.Properties[k]
		if got != want {
			t.Errorf("property %q = %v, want %v", k, got, want)
		}
	}
	if c.Properties["os"] == nil {
		t.Error("missing os property")
	}
	if c.Properties["arch"] == nil {
		t.Error("missing arch property")
	}
	if c.Properties["version"] == nil {
		t.Error("missing version property")
	}
}

func TestTrackCommandFinish_Properties(t *testing.T) {
	dir := enabledDir(t)
	t.Setenv("DO_NOT_TRACK", "")

	var captured []posthog.Capture
	captureHook = func(c posthog.Capture) { captured = append(captured, c) }
	defer func() { captureHook = nil }()

	Init(dir)
	defer Close()

	TrackCommandFinish("claude", 1500, true)

	if len(captured) != 1 {
		t.Fatalf("expected 1 capture, got %d", len(captured))
	}
	c := captured[0]
	if c.Event != "command_finish" {
		t.Errorf("event = %q, want command_finish", c.Event)
	}
	if c.Properties["duration_ms"] != int64(1500) {
		t.Errorf("duration_ms = %v, want 1500", c.Properties["duration_ms"])
	}
	if c.Properties["exit_clean"] != true {
		t.Errorf("exit_clean = %v, want true", c.Properties["exit_clean"])
	}
}

func TestTrack_NilClient(t *testing.T) {
	client = nil
	anonymousID = ""
	Track("test_event", nil)
}

func TestClose_NilClient(t *testing.T) {
	client = nil
	Close()
}

func TestTrack_FeatureEvents(t *testing.T) {
	dir := enabledDir(t)
	t.Setenv("DO_NOT_TRACK", "")

	var captured []posthog.Capture
	captureHook = func(c posthog.Capture) { captured = append(captured, c) }
	defer func() { captureHook = nil }()

	Init(dir)
	defer Close()

	Track("build", map[string]any{"engine": "qemu", "subcommand": "build", "stack": "go", "thin": true})
	Track("init", map[string]any{"engine": "docker", "stack": "base"})
	Track("serve", map[string]any{"port": 8484, "https": true, "pty": false})
	Track("vnc", map[string]any{"viewer": "royaltsx", "global": true})
	Track("rdp", map[string]any{"viewer": "freerdp", "fullscreen": true})
	Track("models", map[string]any{"source": "all"})
	Track("modules_list", nil)
	Track("cleanup", nil)
	Track("auth_kube", map[string]any{"skip_cluster": false})
	Track("auth_chrome", map[string]any{"sync_only": false, "no_sync": false})

	if len(captured) != 10 {
		t.Fatalf("expected 10 captures, got %d", len(captured))
	}

	events := make([]string, len(captured))
	for i, c := range captured {
		events[i] = c.Event
		if c.Properties["os"] == nil {
			t.Errorf("capture %d (%s): missing os property", i, c.Event)
		}
		if c.Properties["version"] == nil {
			t.Errorf("capture %d (%s): missing version property", i, c.Event)
		}
	}

	want := []string{"build", "init", "serve", "vnc", "rdp", "models", "modules_list", "cleanup", "auth_kube", "auth_chrome"}
	for i, w := range want {
		if events[i] != w {
			t.Errorf("event[%d] = %q, want %q", i, events[i], w)
		}
	}

	if captured[0].Properties["engine"] != "qemu" {
		t.Errorf("build.engine = %v, want qemu", captured[0].Properties["engine"])
	}
	if captured[2].Properties["port"] != 8484 {
		t.Errorf("serve.port = %v, want 8484", captured[2].Properties["port"])
	}
}
