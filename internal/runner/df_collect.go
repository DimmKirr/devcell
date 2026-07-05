package runner

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// DFCollector is the test seam for `cell build df` — production wires
// ExecCollector, which shells to `docker system df -v --format json`.
// Tests inject a fake that returns a canned JSON fixture.
type DFCollector interface {
	CollectSystemDF(ctx context.Context) ([]byte, error)
}

// ExecCollector is the real collector. It runs docker once and returns
// the raw JSON for ParseSystemDF to handle.
type ExecCollector struct{}

func (ExecCollector) CollectSystemDF(ctx context.Context) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "docker", "system", "df", "-v", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("docker system df: %w", err)
	}
	return out, nil
}

// DFOpts controls a single `cell build df` invocation.
type DFOpts struct {
	TopN  int         // 0 = all rows
	Kinds []EntryKind // empty = all kinds
	JSON  bool        // true → FormatJSON, false → FormatTable
}

// RunDFArgs bundles inputs to RunDF — keeps the call site readable
// (matches the pattern of RunPruneArgs).
type RunDFArgs struct {
	Ctx       context.Context
	Collector DFCollector
	Opts      DFOpts
	Out       io.Writer
}

// VMDiskInfo holds Docker VM filesystem info from `docker run alpine df`.
type VMDiskInfo struct {
	TotalBytes int64
	UsedBytes  int64
	AvailBytes int64
}

// CollectVMDisk probes the Docker VM filesystem size.
func CollectVMDisk(ctx context.Context) (VMDiskInfo, error) {
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "alpine", "df", "-B1", "/").Output()
	if err != nil {
		return VMDiskInfo{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return VMDiskInfo{}, fmt.Errorf("unexpected df output")
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return VMDiskInfo{}, fmt.Errorf("cannot parse df fields")
	}
	total, _ := strconv.ParseInt(fields[1], 10, 64)
	used, _ := strconv.ParseInt(fields[2], 10, 64)
	avail, _ := strconv.ParseInt(fields[3], 10, 64)
	return VMDiskInfo{TotalBytes: total, UsedBytes: used, AvailBytes: avail}, nil
}

// CollectRunningContainers returns `docker ps` output for display.
func CollectRunningContainers(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "docker", "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Size}}\t{{.Status}}").Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	return lines, nil
}

// CollectVolumeMounts returns a map of volume name → container names that mount it.
func CollectVolumeMounts(ctx context.Context) map[string][]string {
	out, err := exec.CommandContext(ctx, "docker", "ps", "--no-trunc", "--format", "{{.Names}}\t{{.Mounts}}").Output()
	if err != nil {
		return nil
	}
	result := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		name := parts[0]
		for _, mount := range strings.Split(parts[1], ",") {
			vol := strings.TrimSpace(mount)
			if vol != "" {
				result[vol] = append(result[vol], name)
			}
		}
	}
	return result
}

// RunDF orchestrates: collect → parse → format. The pure-data steps live
// in df.go; this function just wires them.
func RunDF(a RunDFArgs) error {
	raw, err := a.Collector.CollectSystemDF(a.Ctx)
	if err != nil {
		return err
	}
	snap, err := ParseSystemDF(raw)
	if err != nil {
		return err
	}
	fopts := FormatOpts{TopN: a.Opts.TopN, Kinds: a.Opts.Kinds}
	if a.Opts.JSON {
		return FormatJSON(snap, fopts, a.Out)
	}

	// Collect VM disk + running containers + volume mounts for the table footer
	vmDisk, _ := CollectVMDisk(a.Ctx)
	containers, _ := CollectRunningContainers(a.Ctx)
	volMounts := CollectVolumeMounts(a.Ctx)
	return FormatTableWithVM(snap, fopts, vmDisk, containers, volMounts, a.Out)
}
