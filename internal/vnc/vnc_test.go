package vnc_test

import (
	"os"
	"testing"

	"github.com/DimmKirr/devcell/internal/vnc"
)

func TestVNCUrl(t *testing.T) {
	got := vnc.VNCUrl("350")
	want := "vnc://:vnc@127.0.0.1:350"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestVNCPasswdFile(t *testing.T) {
	p := vnc.VNCPasswdFile()
	if p == "" {
		t.Fatal("expected non-empty path")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read passwd file: %v", err)
	}
	if len(data) != 8 {
		t.Errorf("expected 8 bytes, got %d", len(data))
	}
}

func TestRoyalTSXVNCUrl(t *testing.T) {
	got := vnc.RoyalTSXVNCUrl("350")
	want := "rtsx://vnc://:vnc@127.0.0.1:350"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestParseDockerPS_Single(t *testing.T) {
	output := "cell-myproject-3-run\t0.0.0.0:350->5900/tcp"
	m, err := vnc.ParseDockerPS(output)
	if err != nil {
		t.Fatal(err)
	}
	if m["myproject-3"] != "350" {
		t.Errorf("want myproject-3→350, got %v", m)
	}
}

func TestParseDockerPS_Multi(t *testing.T) {
	output := "cell-proj-3-run\t0.0.0.0:350->5900/tcp\ncell-other-5-run\t0.0.0.0:550->5900/tcp"
	m, err := vnc.ParseDockerPS(output)
	if err != nil {
		t.Fatal(err)
	}
	if m["proj-3"] != "350" {
		t.Errorf("want proj-3→350, got %v", m)
	}
	if m["other-5"] != "550" {
		t.Errorf("want other-5→550, got %v", m)
	}
}

func TestParseDockerPS_SkipsNon5900(t *testing.T) {
	output := "cell-proj-3-run\t0.0.0.0:8080->80/tcp"
	m, err := vnc.ParseDockerPS(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map for non-5900 port, got %v", m)
	}
}

func TestParseDockerPS_EmptyOutput(t *testing.T) {
	m, err := vnc.ParseDockerPS("")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestParseInspectPort_Valid(t *testing.T) {
	// Minimal docker inspect JSON for port 5900 binding
	inspectJSON := `[{"NetworkSettings":{"Ports":{"5900/tcp":[{"HostIp":"0.0.0.0","HostPort":"350"}]}}}]`
	port, err := vnc.ParseInspectPort(inspectJSON)
	if err != nil {
		t.Fatal(err)
	}
	if port != "350" {
		t.Errorf("want 350, got %q", port)
	}
}

func TestParseInspectPort_Missing(t *testing.T) {
	inspectJSON := `[{"NetworkSettings":{"Ports":{}}}]`
	_, err := vnc.ParseInspectPort(inspectJSON)
	if err == nil {
		t.Error("expected error for missing 5900 port binding")
	}
}

// TestParseDockerPS_DirPortMismatch covers the case where a container was
// started from pane %3 (port 7350) but cell vnc is run from pane %11 (port
// 1150). The lookup must return the actual docker port (7350), not the
// env-computed one (1150).
func TestParseDockerPS_DirPortMismatch(t *testing.T) {
	// Simulate: container started with cellID=3, SESSION_PORT_PREFIX=7
	// Container name: cell-devcell-73-3-run, VNC host port: 7350
	// Current pane: %11 → computed port would be 1150 (wrong)
	dockerPSOutput := "cell-devcell-73-3-run\t0.0.0.0:7350->5900/tcp"
	m, err := vnc.ParseDockerPS(dockerPSOutput)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m["devcell-73-3"]
	if !ok {
		t.Fatalf("expected entry for devcell-73-3, got %v", m)
	}
	if got != "7350" {
		t.Errorf("want actual port 7350, got %q (env-computed would be 1150)", got)
	}
}

// TestParseDockerPS_MultiSession ensures that when multiple sessions of the
// same directory exist, all ports are returned (caller picks one).
func TestParseDockerPS_MultiSession(t *testing.T) {
	output := "cell-devcell-73-3-run\t0.0.0.0:7350->5900/tcp\ncell-devcell-73-5-run\t0.0.0.0:7550->5900/tcp"
	m, err := vnc.ParseDockerPS(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Errorf("expected 2 entries, got %d: %v", len(m), m)
	}
	if m["devcell-73-3"] != "7350" {
		t.Errorf("want devcell-73-3→7350, got %v", m)
	}
	if m["devcell-73-5"] != "7550" {
		t.Errorf("want devcell-73-5→7550, got %v", m)
	}
}
