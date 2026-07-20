// Mock tart CLI for unit tests. Mimics the subset of tart commands that
// devcell uses: --version, clone, get, list, delete. Reads TART_HOME for
// test isolation (real tart does the same — see Config.swift).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func tartHome() string {
	if h := os.Getenv("TART_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fatal("cannot determine home dir: %v", err)
	}
	return filepath.Join(home, ".tart")
}

func vmsDir() string {
	return filepath.Join(tartHome(), "vms")
}

func vmDir(name string) string {
	return filepath.Join(vmsDir(), name)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fatal("usage: tart <command> [args...]")
	}

	if args[0] == "--version" {
		fmt.Println("0.47.0")
		return
	}

	switch args[0] {
	case "clone":
		cmdClone(args[1:])
	case "get":
		cmdGet(args[1:])
	case "list":
		cmdList(args[1:])
	case "delete":
		cmdDelete(args[1:])
	default:
		fatal("unknown command: %s", args[0])
	}
}

func cmdClone(args []string) {
	if len(args) < 2 {
		fatal("usage: tart clone <source> <name>")
	}
	source := args[0]
	name := args[1]

	dir := vmDir(name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fatal("creating vm dir: %v", err)
	}

	cpuCount := 4
	memorySize := uint64(8589934592) // 8 GB
	if strings.Contains(source, "xcode") {
		cpuCount = 8
		memorySize = 17179869184 // 16 GB
	}

	cfg := map[string]any{
		"version":    1,
		"os":         "darwin",
		"arch":       "arm64",
		"cpuCount":   cpuCount,
		"memorySize": memorySize,
		"display":    map[string]int{"width": 1024, "height": 768},
		"macAddress": "aa:bb:cc:dd:ee:ff",
	}
	cfgJSON, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfgJSON, 0644); err != nil {
		fatal("writing config.json: %v", err)
	}

	disk := make([]byte, 1024)
	if err := os.WriteFile(filepath.Join(dir, "disk.img"), disk, 0644); err != nil {
		fatal("writing disk.img: %v", err)
	}

	nvram := make([]byte, 1024)
	if err := os.WriteFile(filepath.Join(dir, "nvram.bin"), nvram, 0644); err != nil {
		fatal("writing nvram.bin: %v", err)
	}

	fmt.Fprintf(os.Stderr, "cloned %s → %s\n", source, name)
}

type getInfo struct {
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

func cmdGet(args []string) {
	if len(args) < 1 {
		fatal("usage: tart get <name> [--format json]")
	}
	name := args[0]

	dir := vmDir(name)
	cfgData, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		fatal("VM %q not found: %v", name, err)
	}

	var cfg struct {
		OS         string `json:"os"`
		CPUCount   int    `json:"cpuCount"`
		MemorySize uint64 `json:"memorySize"`
		Display    struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"display"`
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		fatal("parsing config.json: %v", err)
	}

	diskInfo, err := os.Stat(filepath.Join(dir, "disk.img"))
	diskGB := 0
	sizeGB := "0.001"
	if err == nil {
		diskGB = int(diskInfo.Size() / 1000 / 1000 / 1000)
		sizeGB = fmt.Sprintf("%.3f", float64(diskInfo.Size())/1000/1000/1000)
	}

	info := getInfo{
		OS:         cfg.OS,
		CPU:        cfg.CPUCount,
		Memory:     cfg.MemorySize / 1024 / 1024, // bytes → MB (matches real tart)
		Disk:       diskGB,
		DiskFormat: "raw",
		Size:       sizeGB,
		Display:    fmt.Sprintf("%dx%d", cfg.Display.Width, cfg.Display.Height),
		Running:    false,
		State:      "stopped",
	}

	format := "text"
	for i, a := range args {
		if a == "--format" && i+1 < len(args) {
			format = args[i+1]
		}
	}

	if format == "json" {
		out, _ := json.Marshal(info)
		fmt.Println(string(out))
	} else {
		fmt.Printf("OS:\t%s\nCPU:\t%d\nMemory:\t%d\nDisk:\t%d\n",
			info.OS, info.CPU, info.Memory, info.Disk)
	}
}

type listInfo struct {
	Source  string `json:"Source"`
	Name    string `json:"Name"`
	Disk    int    `json:"Disk"`
	Size    int    `json:"Size"`
	Running bool   `json:"Running"`
	State   string `json:"State"`
}

func cmdList(args []string) {
	dir := vmsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("[]")
			return
		}
		fatal("listing vms: %v", err)
	}

	var infos []listInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cfgPath := filepath.Join(dir, e.Name(), "config.json")
		if _, err := os.Stat(cfgPath); err != nil {
			continue
		}
		infos = append(infos, listInfo{
			Source:  "local",
			Name:    e.Name(),
			Disk:    0,
			Size:    0,
			Running: false,
			State:   "stopped",
		})
	}

	format := "text"
	for i, a := range args {
		if a == "--format" && i+1 < len(args) {
			format = args[i+1]
		}
	}

	if format == "json" {
		if infos == nil {
			infos = []listInfo{}
		}
		out, _ := json.Marshal(infos)
		fmt.Println(string(out))
	} else {
		for _, info := range infos {
			fmt.Printf("%s\t%s\n", info.Source, info.Name)
		}
	}
}

func cmdDelete(args []string) {
	if len(args) < 1 {
		fatal("usage: tart delete <name>")
	}
	for _, name := range args {
		dir := vmDir(name)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			fatal("VM %q does not exist", name)
		}
		if err := os.RemoveAll(dir); err != nil {
			fatal("deleting VM %q: %v", name, err)
		}
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
	os.Exit(1)
}
