// White-box tests (package config) for docker-aware port resolution — CELL-119.
// These exercise unexported helpers that the external config_test.go can't reach.
package config

import (
	"strconv"
	"testing"
)

func TestParseDockerPublishedPorts_TwoMappings(t *testing.T) {
	in := "0.0.0.0:25689->3389/tcp, 0.0.0.0:25650->5900/tcp"
	got := parseDockerPublishedPorts(in)
	for _, want := range []int{25689, 25650} {
		if _, ok := got[want]; !ok {
			t.Errorf("want port %d in set, got %v", want, got)
		}
	}
	if len(got) != 2 {
		t.Errorf("want 2 ports, got %d: %v", len(got), got)
	}
}

func TestParseDockerPublishedPorts_IPv6(t *testing.T) {
	got := parseDockerPublishedPorts(":::10089->3389/tcp")
	if _, ok := got[10089]; !ok {
		t.Errorf("want 10089 from IPv6 mapping, got %v", got)
	}
}

func TestParseDockerPublishedPorts_Empty(t *testing.T) {
	got := parseDockerPublishedPorts("")
	if len(got) != 0 {
		t.Errorf("empty input should yield empty set, got %v", got)
	}
}

// Core regression: a port that net.Listen reports free but docker has already
// allocated (the Docker Desktop case) must be skipped by the scan.
func TestResolveAvailablePort_BumpsOffDockerAllocated(t *testing.T) {
	taken := map[int]struct{}{10089: {}}
	// preferred "89" → <1024 → hoist +10000 → 10089, which is in `taken`.
	got := resolveAvailablePort("89", taken)
	if got == "10089" {
		t.Fatalf("should have bumped off docker-allocated 10089, got %q", got)
	}
	n, err := strconv.Atoi(got)
	if err != nil || n < 10090 {
		t.Fatalf("want a port >= 10090, got %q (err=%v)", got, err)
	}
}

func TestResolveAvailablePort_EmptyTakenPreservesBehavior(t *testing.T) {
	// 4250 is ≥1024 and almost certainly free in test → unchanged.
	got := resolveAvailablePort("4250", map[int]struct{}{})
	if got != "4250" {
		t.Errorf("empty taken set should preserve net.Listen behavior: want 4250, got %q", got)
	}
}
