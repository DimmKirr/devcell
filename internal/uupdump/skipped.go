package uupdump

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// SummarizeSkipped describes the files a fetch discarded, grouped by extension.
//
// FetchWindowsISO downloads only `.esd` and drops the rest. That is consistent
// with what AssembleISO can consume — it turns an ESD into a bootable installer
// and speaks nothing else — but for the current ARM64 build it means discarding
// 45 of 65 files and 4.2 GB, *more than it keeps*: the cumulative update, the
// WSL2 prerequisite (HyperV-OptionalFeature-VirtualMachinePlatform), .NET 4.8.1,
// WebView2, and the FoD metadata cabs.
//
// The decision is defensible; being silent about it is not. Without this line a
// build produces an image missing those payloads and says nothing, so the gap
// surfaces hours later inside a guest as `0x80070002 — cannot find the file
// specified`, which reads like a missing file path rather than a payload that
// was never downloaded.
func SummarizeSkipped(files map[string]File, keepExt string) string {
	counts := map[string]int{}
	sizes := map[string]int64{}
	var total int64
	var n int

	for name, f := range files {
		ext := strings.ToLower(filepath.Ext(name))
		if ext == keepExt {
			continue
		}
		if ext == "" {
			ext = "(none)"
		}
		counts[ext]++
		sizes[ext] += f.Size
		total += f.Size
		n++
	}

	if n == 0 {
		return "skipped nothing: every file in the package was kept"
	}

	exts := make([]string, 0, len(counts))
	for e := range counts {
		exts = append(exts, e)
	}
	// Largest first: the size is the point, so lead with what costs most.
	sort.Slice(exts, func(i, j int) bool { return sizes[exts[i]] > sizes[exts[j]] })

	parts := make([]string, 0, len(exts))
	for _, e := range exts {
		parts = append(parts, fmt.Sprintf("%d %s", counts[e], e))
	}
	return fmt.Sprintf("skipped %d files (%s), %.1f GB not downloaded",
		n, strings.Join(parts, ", "), float64(total)/(1024*1024*1024))
}
