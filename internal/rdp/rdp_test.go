package rdp_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestRoyalTSXUrl(t *testing.T) {
	got := rdp.RoyalTSXUrl("389", "dmitry", "rdp")
	want := "rtsx://rdp://dmitry:rdp@127.0.0.1:389"
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

// mockLookPath returns a lookPath that "finds" only the given binaries.
func mockLookPath(available ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, b := range available {
		set[b] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", fmt.Errorf("not found: %s", name)
	}
}

func TestFindClient_DarwinPrefersSDL(t *testing.T) {
	// Both sdl and x11 available — macOS should prefer sdl-freerdp3
	c, ok := rdp.FindClientWith("darwin", mockLookPath("xfreerdp", "sdl-freerdp3"))
	if !ok {
		t.Fatal("expected to find client")
	}
	if c.Name != "sdl-freerdp3" {
		t.Errorf("darwin should prefer sdl-freerdp3, got %q", c.Name)
	}
}

func TestFindClient_LinuxPrefersX11(t *testing.T) {
	// Both sdl and x11 available — Linux should prefer xfreerdp3
	c, ok := rdp.FindClientWith("linux", mockLookPath("sdl-freerdp3", "xfreerdp3"))
	if !ok {
		t.Fatal("expected to find client")
	}
	if c.Name != "xfreerdp3" {
		t.Errorf("linux should prefer xfreerdp3, got %q", c.Name)
	}
}

func TestFindClient_FallsBackToV2(t *testing.T) {
	// Only v2 available
	c, ok := rdp.FindClientWith("darwin", mockLookPath("sdl-freerdp"))
	if !ok {
		t.Fatal("expected to find client")
	}
	if c.Name != "sdl-freerdp" {
		t.Errorf("should fall back to sdl-freerdp, got %q", c.Name)
	}
}

func TestFindClient_NoneAvailable(t *testing.T) {
	_, ok := rdp.FindClientWith("darwin", mockLookPath())
	if ok {
		t.Error("expected no client found")
	}
}

func TestFindClient_LinuxFallsToSDL(t *testing.T) {
	// Only SDL available on Linux — should still find it
	c, ok := rdp.FindClientWith("linux", mockLookPath("sdl-freerdp3"))
	if !ok {
		t.Fatal("expected to find client")
	}
	if c.Name != "sdl-freerdp3" {
		t.Errorf("linux should fall back to sdl-freerdp3, got %q", c.Name)
	}
}

func TestCertFingerprint_ValidCert(t *testing.T) {
	// Generate a self-signed cert in a temp dir
	dir := t.TempDir()
	xrdpDir := filepath.Join(dir, "xrdp")
	os.MkdirAll(xrdpDir, 0700)
	out, err := exec.Command("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
		"-keyout", filepath.Join(xrdpDir, "key.pem"),
		"-out", filepath.Join(xrdpDir, "cert.pem"),
		"-days", "1", "-subj", "/CN=test").CombinedOutput()
	if err != nil {
		t.Skipf("openssl not available: %v\n%s", err, out)
	}
	fp := rdp.CertFingerprint(dir)
	if fp == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	// SHA256 fingerprint = 32 bytes = 32 hex pairs with colons
	parts := strings.Split(fp, ":")
	if len(parts) != 32 {
		t.Errorf("expected 32 colon-separated hex bytes, got %d: %s", len(parts), fp)
	}
}

func TestCertFingerprint_MissingCert(t *testing.T) {
	fp := rdp.CertFingerprint(t.TempDir())
	if fp != "" {
		t.Errorf("expected empty fingerprint for missing cert, got %q", fp)
	}
}

func TestCertFlag_WithCert(t *testing.T) {
	dir := t.TempDir()
	xrdpDir := filepath.Join(dir, "xrdp")
	os.MkdirAll(xrdpDir, 0700)
	out, err := exec.Command("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
		"-keyout", filepath.Join(xrdpDir, "key.pem"),
		"-out", filepath.Join(xrdpDir, "cert.pem"),
		"-days", "1", "-subj", "/CN=test").CombinedOutput()
	if err != nil {
		t.Skipf("openssl not available: %v\n%s", err, out)
	}
	flag := rdp.CertFlag(dir)
	if !strings.HasPrefix(flag, "/cert:fingerprint:sha256:") {
		t.Errorf("expected fingerprint flag, got %q", flag)
	}
}

func TestCertFlag_WithoutCert(t *testing.T) {
	flag := rdp.CertFlag(t.TempDir())
	if flag != "/cert:ignore" {
		t.Errorf("expected /cert:ignore fallback, got %q", flag)
	}
}
