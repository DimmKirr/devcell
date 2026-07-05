package main

import (
	"errors"
	"slices"
	"testing"
)

// kickMcpInCellsSharingCellHome iterates running cell-* containers, finds
// those whose /home/<HostUser> mount source equals cellHome, and SIGTERMs
// mcp-server-patchright inside them. Best-effort — never returns an error
// that would block `cell auth chrome`.
//
// Helper is dependency-injected so tests don't shell out to docker.

func TestKickMcp_NoContainers_NoKills(t *testing.T) {
	got := kickMcpInCellsSharingCellHome(kickDeps{
		cellHome:       "/Users/dmitry/.devcell/FAM",
		hostUser:       "dmitry",
		listContainers: func() ([]string, error) { return nil, nil },
		mountSource:    func(string) (string, error) { return "", nil },
		killMcp:        func(string) error { return nil },
	})
	if len(got) != 0 {
		t.Errorf("kicked = %v, want empty (no containers running)", got)
	}
}

func TestKickMcp_OneMatchingContainer_Kicked(t *testing.T) {
	var killed []string
	got := kickMcpInCellsSharingCellHome(kickDeps{
		cellHome:       "/Users/dmitry/.devcell/FAM",
		hostUser:       "dmitry",
		listContainers: func() ([]string, error) { return []string{"abc123"}, nil },
		mountSource: func(id string) (string, error) {
			if id == "abc123" {
				return "/Users/dmitry/.devcell/FAM", nil
			}
			return "", nil
		},
		killMcp: func(id string) error {
			killed = append(killed, id)
			return nil
		},
	})
	want := []string{"abc123"}
	if !slices.Equal(got, want) {
		t.Errorf("kicked = %v, want %v", got, want)
	}
	if !slices.Equal(killed, want) {
		t.Errorf("killMcp called with = %v, want %v", killed, want)
	}
}

func TestKickMcp_NonMatchingMountSource_Skipped(t *testing.T) {
	var killed []string
	got := kickMcpInCellsSharingCellHome(kickDeps{
		cellHome:       "/Users/dmitry/.devcell/FAM",
		hostUser:       "dmitry",
		listContainers: func() ([]string, error) { return []string{"def456"}, nil },
		mountSource: func(string) (string, error) {
			return "/Users/dmitry/.devcell/DIMM", nil // different session
		},
		killMcp: func(id string) error {
			killed = append(killed, id)
			return nil
		},
	})
	if len(got) != 0 {
		t.Errorf("kicked = %v, want empty (container is in a different session)", got)
	}
	if len(killed) != 0 {
		t.Errorf("killMcp should not have been called, got %v", killed)
	}
}

func TestKickMcp_MixedContainers_OnlyMatchingKicked(t *testing.T) {
	var killed []string
	mounts := map[string]string{
		"a1": "/Users/dmitry/.devcell/FAM",  // match
		"b2": "/Users/dmitry/.devcell/DIMM", // skip
		"c3": "/Users/dmitry/.devcell/FAM",  // match
	}
	got := kickMcpInCellsSharingCellHome(kickDeps{
		cellHome:       "/Users/dmitry/.devcell/FAM",
		hostUser:       "dmitry",
		listContainers: func() ([]string, error) { return []string{"a1", "b2", "c3"}, nil },
		mountSource:    func(id string) (string, error) { return mounts[id], nil },
		killMcp: func(id string) error {
			killed = append(killed, id)
			return nil
		},
	})
	want := []string{"a1", "c3"}
	if !slices.Equal(got, want) {
		t.Errorf("kicked = %v, want %v", got, want)
	}
	if !slices.Equal(killed, want) {
		t.Errorf("killMcp called with = %v, want %v", killed, want)
	}
}

func TestKickMcp_ListContainersError_ReturnsEmpty(t *testing.T) {
	got := kickMcpInCellsSharingCellHome(kickDeps{
		cellHome:       "/Users/dmitry/.devcell/FAM",
		hostUser:       "dmitry",
		listContainers: func() ([]string, error) { return nil, errors.New("docker daemon down") },
		mountSource:    func(string) (string, error) { t.Fatal("should not be called"); return "", nil },
		killMcp:        func(string) error { t.Fatal("should not be called"); return nil },
	})
	if len(got) != 0 {
		t.Errorf("kicked = %v, want empty when listContainers fails", got)
	}
}

func TestKickMcp_MountSourceError_SkipsContainer(t *testing.T) {
	var killed []string
	got := kickMcpInCellsSharingCellHome(kickDeps{
		cellHome:       "/Users/dmitry/.devcell/FAM",
		hostUser:       "dmitry",
		listContainers: func() ([]string, error) { return []string{"x", "y"}, nil },
		mountSource: func(id string) (string, error) {
			if id == "x" {
				return "", errors.New("inspect failed")
			}
			return "/Users/dmitry/.devcell/FAM", nil
		},
		killMcp: func(id string) error {
			killed = append(killed, id)
			return nil
		},
	})
	want := []string{"y"}
	if !slices.Equal(got, want) {
		t.Errorf("kicked = %v, want %v (x failed inspect, y proceeded)", got, want)
	}
}

// killMcp error → container still reported as "attempted" so the caller can
// log the partial outcome, but execution continues for remaining containers.
func TestKickMcp_KillError_ContinuesWithOtherContainers(t *testing.T) {
	var killAttempts []string
	got := kickMcpInCellsSharingCellHome(kickDeps{
		cellHome:       "/Users/dmitry/.devcell/FAM",
		hostUser:       "dmitry",
		listContainers: func() ([]string, error) { return []string{"p", "q"}, nil },
		mountSource:    func(string) (string, error) { return "/Users/dmitry/.devcell/FAM", nil },
		killMcp: func(id string) error {
			killAttempts = append(killAttempts, id)
			if id == "p" {
				return errors.New("pkill exit 1") // patchright wasn't running in p
			}
			return nil
		},
	})
	// Both attempted, both reported (caller can distinguish via debug logs if needed)
	want := []string{"p", "q"}
	if !slices.Equal(killAttempts, want) {
		t.Errorf("killMcp attempts = %v, want %v", killAttempts, want)
	}
	if !slices.Equal(got, want) {
		t.Errorf("kicked = %v, want %v (best-effort: both reported)", got, want)
	}
}
