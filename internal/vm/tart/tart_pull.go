package tart

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TartConfig represents the VM configuration from a Tart config.json.
type TartConfig struct {
	OS         string      `json:"os"`
	Arch       string      `json:"arch"`
	CPUCount   int         `json:"cpuCount"`
	MemorySize uint64      `json:"memorySize"`
	Display    TartDisplay `json:"display"`
	MACAddress string      `json:"macAddress"`
}

// TartDisplay holds display resolution from a Tart config.
type TartDisplay struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// MemoryGB returns memory size in whole gigabytes.
func (c TartConfig) MemoryGB() uint64 {
	return c.MemorySize / (1024 * 1024 * 1024)
}

// ParseTartConfig parses a Tart VM config JSON blob.
func ParseTartConfig(data []byte) (TartConfig, error) {
	var cfg TartConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return TartConfig{}, fmt.Errorf("parsing tart config: %w", err)
	}
	return cfg, nil
}

// ToSpec converts a TartConfig to a tart Spec.
func (c TartConfig) ToSpec(artifactDir string) Spec {
	return Spec{
		CPUs:     uint(c.CPUCount),
		MemoryGB: c.MemoryGB(),
		DiskPath: filepath.Join(artifactDir, "disk.img"),
		AuxPath:  filepath.Join(artifactDir, "aux-storage.img"),
		MACAddr:  c.MACAddress,
	}
}

// TartPreflight verifies the tart binary is installed and returns its version.
// Parallel to Docker: exec.LookPath("docker") + `docker version`.
func TartPreflight() (string, error) {
	path, err := exec.LookPath("tart")
	if err != nil {
		return "", fmt.Errorf("tart not found; install with: brew install cirruslabs/cli/tart")
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("running tart --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// TartGetInfo is the JSON output of `tart get <name> --format json`.
// Parallel to Docker: `docker image inspect <tag>`.
type TartGetInfo struct {
	OS         string `json:"OS"`
	CPU        int    `json:"CPU"`
	Memory     uint64 `json:"Memory"`
	Disk       int    `json:"Disk"`
	DiskFormat string `json:"DiskFormat"`
	Size       string `json:"Size"`
	Display    string `json:"Display"`
	Running    bool   `json:"Running"`
	State      string `json:"State"`
}

// TartClone runs `tart clone <ref> <localName>`.
// Parallel to Docker: exec.Command("docker", "pull", tag).
func TartClone(ctx context.Context, ref, localName string) error {
	cmd := exec.CommandContext(ctx, "tart", "clone", ref, localName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// TartGet runs `tart get <name> --format json` and returns the parsed info.
// Parallel to Docker: exec.Command("docker", "image", "inspect", tag).
func TartGet(ctx context.Context, name string) (TartGetInfo, error) {
	out, err := exec.CommandContext(ctx, "tart", "get", name, "--format", "json").Output()
	if err != nil {
		return TartGetInfo{}, fmt.Errorf("tart get %s: %w", name, err)
	}
	var info TartGetInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return TartGetInfo{}, fmt.Errorf("parsing tart get output: %w", err)
	}
	return info, nil
}

// TartDelete runs `tart delete <name>`.
// Parallel to Docker: exec.Command("docker", "rm", name).
func TartDelete(ctx context.Context, name string) error {
	return exec.CommandContext(ctx, "tart", "delete", name).Run()
}

// tartHome returns the tart home directory, respecting TART_HOME env var.
// Real tart: Config.swift reads ProcessInfo.processInfo.environment["TART_HOME"].
func tartHome() (string, error) {
	if h := os.Getenv("TART_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	return filepath.Join(home, ".tart"), nil
}

// AcquireFromTart clones a Tart OCI image to local storage and returns the
// parsed VM config plus the VM directory path (containing disk.img, nvram.bin,
// config.json). The caller uses ToSpec(vmDir) to build a Spec for booting.
//
// Parallel to the old ExtractTartImage which pulled OCI layers via
// go-containerregistry. This shells out to `tart clone` — same pattern as
// Docker's exec.Command("docker", "pull", tag) in runner.go.
func AcquireFromTart(ctx context.Context, ref, localName string) (TartConfig, string, error) {
	if err := TartClone(ctx, ref, localName); err != nil {
		return TartConfig{}, "", fmt.Errorf("tart clone %s → %s: %w", ref, localName, err)
	}

	th, err := tartHome()
	if err != nil {
		return TartConfig{}, "", err
	}
	vmDir := filepath.Join(th, "vms", localName)

	cfgData, err := os.ReadFile(filepath.Join(vmDir, "config.json"))
	if err != nil {
		return TartConfig{}, "", fmt.Errorf("reading config.json from %s: %w", vmDir, err)
	}
	cfg, err := ParseTartConfig(cfgData)
	if err != nil {
		return TartConfig{}, "", err
	}
	return cfg, vmDir, nil
}
