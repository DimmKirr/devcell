package uupdump

import (
	"strings"
	"testing"
)

// FetchWindowsISO keeps only .esd and discards everything else without a word.
// For the current ARM64 build that is 45 of 65 files and 4.2 GB — more than it
// keeps — including the cumulative update (.msu), the WSL2 prerequisite
// (HyperV-OptionalFeature-VirtualMachinePlatform), .NET 4.8.1 and WebView2.
//
// The decision may be defensible (the assembler only speaks ESD), but it must
// not be invisible: a build produces an image missing those payloads and says
// nothing, so the gap only surfaces hours later inside a guest as
// `0x80070002 — cannot find the file specified`.
func TestSummarizeSkipped_NamesEveryExtensionAndTheTotal(t *testing.T) {
	files := map[string]File{
		"install.esd":                       {Size: 3_000_000_000},
		"Windows11.0-KB5101681-arm64.msu":   {Size: 3_315_000_000},
		"HyperV-VirtualMachinePlatform.cab": {Size: 12_000_000},
		"OpenSSH-Client-Package-arm64.cab":  {Size: 1_000_000},
		"Edge.wim":                          {Size: 201_000_000},
	}

	summary := SummarizeSkipped(files, ".esd")

	for _, want := range []string{"3 .cab", "1 .msu", "1 .wim"} {
		if !strings.Contains(summary, want) {
			// .cab count is 2 here; assert the shape below instead.
			_ = want
		}
	}
	if !strings.Contains(summary, "2 .cab") {
		t.Errorf("summary must count each extension, got %q", summary)
	}
	if !strings.Contains(summary, "1 .msu") || !strings.Contains(summary, "1 .wim") {
		t.Errorf("summary must name every skipped extension, got %q", summary)
	}
	if !strings.Contains(summary, "GB") {
		t.Errorf("summary must state the total size — the point is the magnitude, got %q", summary)
	}
	if !strings.Contains(summary, "4 files") {
		t.Errorf("summary must state how many files were skipped, got %q", summary)
	}
}

// Nothing skipped is worth saying too: silence would be ambiguous between
// "kept everything" and "the summary is broken".
func TestSummarizeSkipped_SaysSoWhenNothingWasDropped(t *testing.T) {
	summary := SummarizeSkipped(map[string]File{"a.esd": {Size: 10}}, ".esd")

	if !strings.Contains(strings.ToLower(summary), "nothing") {
		t.Errorf("an empty skip set must be stated explicitly, got %q", summary)
	}
}
