package tart

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var mockTartBin string

func TestMain(m *testing.M) {
	tmpBin, err := os.MkdirTemp("", "mocktart-bin-*")
	if err != nil {
		log.Fatalf("creating temp dir: %v", err)
	}

	mockTartBin = filepath.Join(tmpBin, "tart")
	build := exec.Command("go", "build", "-o", mockTartBin, "./testdata/mocktart")
	if out, err := build.CombinedOutput(); err != nil {
		log.Fatalf("building mock tart:\n%s\n%v", out, err)
	}

	os.Setenv("PATH", tmpBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	code := m.Run()
	os.RemoveAll(tmpBin)
	os.Exit(code)
}

// --- ParseTartConfig / MemoryGB / ToSpec (unchanged, no tart binary needed) ---

func TestParseTartConfig(t *testing.T) {
	raw := []byte(`{
		"os": "darwin",
		"arch": "arm64",
		"cpuCount": 4,
		"memorySize": 8589934592,
		"display": {"width": 1920, "height": 1080},
		"macAddress": "aa:bb:cc:dd:ee:ff"
	}`)

	cfg, err := ParseTartConfig(raw)
	if err != nil {
		t.Fatalf("ParseTartConfig() error: %v", err)
	}
	if cfg.OS != "darwin" {
		t.Errorf("OS = %q, want %q", cfg.OS, "darwin")
	}
	if cfg.Arch != "arm64" {
		t.Errorf("Arch = %q, want %q", cfg.Arch, "arm64")
	}
	if cfg.CPUCount != 4 {
		t.Errorf("CPUCount = %d, want 4", cfg.CPUCount)
	}
	if cfg.MemorySize != 8589934592 {
		t.Errorf("MemorySize = %d, want 8589934592", cfg.MemorySize)
	}
	if cfg.Display.Width != 1920 {
		t.Errorf("Display.Width = %d, want 1920", cfg.Display.Width)
	}
	if cfg.Display.Height != 1080 {
		t.Errorf("Display.Height = %d, want 1080", cfg.Display.Height)
	}
	if cfg.MACAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MACAddress = %q, want %q", cfg.MACAddress, "aa:bb:cc:dd:ee:ff")
	}
}

func TestParseTartConfigMemoryGB(t *testing.T) {
	raw := []byte(`{
		"os": "darwin",
		"arch": "arm64",
		"cpuCount": 8,
		"memorySize": 17179869184,
		"display": {"width": 1024, "height": 768},
		"macAddress": "00:11:22:33:44:55"
	}`)

	cfg, err := ParseTartConfig(raw)
	if err != nil {
		t.Fatalf("ParseTartConfig() error: %v", err)
	}
	got := cfg.MemoryGB()
	if got != 16 {
		t.Errorf("MemoryGB() = %d, want 16", got)
	}
}

func TestParseTartConfigInvalidJSON(t *testing.T) {
	_, err := ParseTartConfig([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseTartConfigMissingFields(t *testing.T) {
	raw := []byte(`{"os": "darwin"}`)
	cfg, err := ParseTartConfig(raw)
	if err != nil {
		t.Fatalf("ParseTartConfig() error: %v", err)
	}
	if cfg.CPUCount != 0 {
		t.Errorf("CPUCount = %d, want 0 for missing field", cfg.CPUCount)
	}
}

func TestTartConfigToSpec(t *testing.T) {
	cfg := TartConfig{
		OS:         "darwin",
		Arch:       "arm64",
		CPUCount:   6,
		MemorySize: 8589934592,
		Display:    TartDisplay{Width: 1920, Height: 1080},
		MACAddress: "aa:bb:cc:dd:ee:ff",
	}

	spec := cfg.ToSpec("/path/to/artifacts")
	if spec.CPUs != 6 {
		t.Errorf("CPUs = %d, want 6", spec.CPUs)
	}
	if spec.MemoryGB != 8 {
		t.Errorf("MemoryGB = %d, want 8", spec.MemoryGB)
	}
	if spec.MACAddr != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MACAddr = %q, want %q", spec.MACAddr, "aa:bb:cc:dd:ee:ff")
	}
	if spec.DiskPath != "/path/to/artifacts/disk.img" {
		t.Errorf("DiskPath = %q, want /path/to/artifacts/disk.img", spec.DiskPath)
	}
}

// --- TartPreflight ---

func TestTartPreflight_Present(t *testing.T) {
	ver, err := TartPreflight()
	if err != nil {
		t.Fatalf("TartPreflight() error: %v", err)
	}
	if ver != "0.47.0" {
		t.Errorf("version = %q, want %q", ver, "0.47.0")
	}
}

func TestTartPreflight_Missing(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")

	_, err := TartPreflight()
	if err == nil {
		t.Fatal("expected error when tart is not on PATH")
	}
	if !strings.Contains(err.Error(), "brew install") {
		t.Errorf("error should mention brew install, got: %v", err)
	}
}

// --- TartClone ---

func TestTartClone(t *testing.T) {
	th := t.TempDir()
	t.Setenv("TART_HOME", th)

	err := TartClone(context.Background(), "ghcr.io/cirruslabs/macos-sequoia-base:latest", "test-vm")
	if err != nil {
		t.Fatalf("TartClone() error: %v", err)
	}

	vmDir := filepath.Join(th, "vms", "test-vm")
	for _, f := range []string{"config.json", "disk.img", "nvram.bin"} {
		if _, err := os.Stat(filepath.Join(vmDir, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}

	cfgData, err := os.ReadFile(filepath.Join(vmDir, "config.json"))
	if err != nil {
		t.Fatalf("reading config.json: %v", err)
	}
	var cfg TartConfig
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("parsing config.json: %v", err)
	}
	if cfg.OS != "darwin" {
		t.Errorf("config OS = %q, want darwin", cfg.OS)
	}
	if cfg.CPUCount != 4 {
		t.Errorf("config CPUCount = %d, want 4", cfg.CPUCount)
	}
}

// --- TartGet ---

func TestTartGet(t *testing.T) {
	th := t.TempDir()
	t.Setenv("TART_HOME", th)

	if err := TartClone(context.Background(), "ghcr.io/test:latest", "get-vm"); err != nil {
		t.Fatalf("setup clone: %v", err)
	}

	info, err := TartGet(context.Background(), "get-vm")
	if err != nil {
		t.Fatalf("TartGet() error: %v", err)
	}
	if info.OS != "darwin" {
		t.Errorf("OS = %q, want darwin", info.OS)
	}
	if info.CPU != 4 {
		t.Errorf("CPU = %d, want 4", info.CPU)
	}
	if info.State != "stopped" {
		t.Errorf("State = %q, want stopped", info.State)
	}
}

// --- TartDelete ---

func TestTartDelete(t *testing.T) {
	th := t.TempDir()
	t.Setenv("TART_HOME", th)

	if err := TartClone(context.Background(), "ghcr.io/test:latest", "del-vm"); err != nil {
		t.Fatalf("setup clone: %v", err)
	}

	vmDir := filepath.Join(th, "vms", "del-vm")
	if _, err := os.Stat(vmDir); err != nil {
		t.Fatalf("VM dir should exist before delete: %v", err)
	}

	if err := TartDelete(context.Background(), "del-vm"); err != nil {
		t.Fatalf("TartDelete() error: %v", err)
	}

	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Errorf("VM dir should be gone after delete, got err: %v", err)
	}
}

// --- AcquireFromTart ---

func TestAcquireFromTart(t *testing.T) {
	th := t.TempDir()
	t.Setenv("TART_HOME", th)

	cfg, vmDir, err := AcquireFromTart(context.Background(), "ghcr.io/cirruslabs/macos-sequoia-base:latest", "acquire-vm")
	if err != nil {
		t.Fatalf("AcquireFromTart() error: %v", err)
	}

	if cfg.OS != "darwin" {
		t.Errorf("OS = %q, want darwin", cfg.OS)
	}
	if cfg.Arch != "arm64" {
		t.Errorf("Arch = %q, want arm64", cfg.Arch)
	}
	if cfg.CPUCount != 4 {
		t.Errorf("CPUCount = %d, want 4", cfg.CPUCount)
	}
	if cfg.MemorySize != 8589934592 {
		t.Errorf("MemorySize = %d, want 8589934592", cfg.MemorySize)
	}
	if cfg.MACAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MACAddress = %q, want aa:bb:cc:dd:ee:ff", cfg.MACAddress)
	}

	wantDir := filepath.Join(th, "vms", "acquire-vm")
	if vmDir != wantDir {
		t.Errorf("vmDir = %q, want %q", vmDir, wantDir)
	}

	spec := cfg.ToSpec(vmDir)
	if spec.CPUs != 4 {
		t.Errorf("spec.CPUs = %d, want 4", spec.CPUs)
	}
	if spec.DiskPath != filepath.Join(vmDir, "disk.img") {
		t.Errorf("spec.DiskPath = %q, want %q", spec.DiskPath, filepath.Join(vmDir, "disk.img"))
	}
}

// --- Long integration test (requires real tart + macOS) ---

func TestTartPullRealImage(t *testing.T) {
	if testing.Short() {
		t.Skip("long: clones Tart macOS image from ghcr.io/cirruslabs (~15 GB compressed); requires tart CLI")
	}

	const ref = "ghcr.io/cirruslabs/macos-sequoia-base:latest"
	const name = "devcell-test-pull"

	ver, err := TartPreflight()
	if err != nil {
		t.Skipf("tart not available: %v", err)
	}
	t.Logf("tart version: %s", ver)

	defer TartDelete(context.Background(), name)

	cfg, vmDir, err := AcquireFromTart(context.Background(), ref, name)
	if err != nil {
		t.Fatalf("AcquireFromTart(%s): %v", ref, err)
	}

	if cfg.OS != "darwin" {
		t.Errorf("OS = %q, want darwin", cfg.OS)
	}
	if cfg.Arch != "arm64" {
		t.Errorf("Arch = %q, want arm64", cfg.Arch)
	}
	if cfg.CPUCount == 0 {
		t.Error("CPUCount = 0, want > 0")
	}
	if cfg.MemorySize == 0 {
		t.Error("MemorySize = 0, want > 0")
	}

	diskInfo, err := os.Stat(filepath.Join(vmDir, "disk.img"))
	if err != nil {
		t.Fatalf("disk.img not found: %v", err)
	}
	const minDiskSize = 1024 * 1024 * 1024
	if diskInfo.Size() < minDiskSize {
		t.Errorf("disk.img too small: %d bytes (want >= %d)", diskInfo.Size(), minDiskSize)
	}

	spec := cfg.ToSpec(vmDir)
	spec.ApplyDefaults()
	if err := spec.Validate(); err != nil {
		t.Errorf("ToSpec().Validate(): %v", err)
	}

	t.Logf("cloned %s: OS=%s Arch=%s CPUs=%d Mem=%dGB disk=%d MB",
		ref, cfg.OS, cfg.Arch, cfg.CPUCount, cfg.MemoryGB(),
		diskInfo.Size()/(1024*1024))
}
