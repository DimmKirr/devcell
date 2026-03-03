package rdp_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/rdp"
)

func TestRDPUrl(t *testing.T) {
	got := rdp.RDPUrl("389")
	want := "rdp://full%20address=s%3A127.0.0.1%3A389"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestParseDockerPS_Single(t *testing.T) {
	output := "cell-myproject-3-run\t0.0.0.0:389->3389/tcp"
	m, err := rdp.ParseDockerPS(output)
	if err != nil {
		t.Fatal(err)
	}
	if m["myproject-3"] != "389" {
		t.Errorf("want myproject-3→389, got %v", m)
	}
}

func TestParseDockerPS_Multi(t *testing.T) {
	output := "cell-proj-3-run\t0.0.0.0:389->3389/tcp\ncell-other-5-run\t0.0.0.0:589->3389/tcp"
	m, err := rdp.ParseDockerPS(output)
	if err != nil {
		t.Fatal(err)
	}
	if m["proj-3"] != "389" {
		t.Errorf("want proj-3→389, got %v", m)
	}
	if m["other-5"] != "589" {
		t.Errorf("want other-5→589, got %v", m)
	}
}

func TestParseDockerPS_SkipsNon3389(t *testing.T) {
	output := "cell-proj-3-run\t0.0.0.0:8080->80/tcp"
	m, err := rdp.ParseDockerPS(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map for non-3389 port, got %v", m)
	}
}

func TestParseDockerPS_EmptyOutput(t *testing.T) {
	m, err := rdp.ParseDockerPS("")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestParseInspectPort_Valid(t *testing.T) {
	inspectJSON := `[{"NetworkSettings":{"Ports":{"3389/tcp":[{"HostIp":"0.0.0.0","HostPort":"389"}]}}}]`
	port, err := rdp.ParseInspectPort(inspectJSON)
	if err != nil {
		t.Fatal(err)
	}
	if port != "389" {
		t.Errorf("want 389, got %q", port)
	}
}

func TestParseInspectPort_Missing(t *testing.T) {
	inspectJSON := `[{"NetworkSettings":{"Ports":{}}}]`
	_, err := rdp.ParseInspectPort(inspectJSON)
	if err == nil {
		t.Error("expected error for missing 3389 port binding")
	}
}

func TestFindContainersByBind_Match(t *testing.T) {
	inspectJSON := `[{
		"Name": "/cell-myproject-3-run",
		"HostConfig": {"Binds": ["/tmp/myproject:/tmp/myproject"]},
		"NetworkSettings": {"Ports": {"3389/tcp": [{"HostIp": "0.0.0.0", "HostPort": "389"}]}}
	}]`
	matches, err := rdp.FindContainersByBind(inspectJSON, "/tmp/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Port != "389" || matches[0].AppName != "myproject-3" {
		t.Errorf("unexpected match: %+v", matches[0])
	}
}

func TestFindContainersByBind_NoRDP(t *testing.T) {
	inspectJSON := `[{
		"Name": "/cell-myproject-3-run",
		"HostConfig": {"Binds": ["/tmp/myproject:/tmp/myproject"]},
		"NetworkSettings": {"Ports": {"5900/tcp": [{"HostIp": "0.0.0.0", "HostPort": "350"}]}}
	}]`
	matches, err := rdp.FindContainersByBind(inspectJSON, "/tmp/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("expected 0 matches (no 3389 port), got %d", len(matches))
	}
}
