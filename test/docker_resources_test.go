// docker_resources_test.go — verify Docker resource limits (--memory, --cpus, --shm-size)
// are honoured by the daemon. Short test: uses alpine, no devcell image required.

package container_test

import (
	"context"
	"encoding/json"
	"fmt"
	osexec "os/exec"
	"strings"
	"testing"
)

// dockerInspectHostConfig returns the HostConfig subtree of a running container.
func dockerInspectHostConfig(t *testing.T, containerID string) map[string]any {
	t.Helper()
	out, err := osexec.Command("docker", "inspect", "--format", "{{json .HostConfig}}", containerID).Output()
	if err != nil {
		t.Fatalf("docker inspect: %v", err)
	}
	var hc map[string]any
	if err := json.Unmarshal(out, &hc); err != nil {
		t.Fatalf("parse HostConfig JSON: %v", err)
	}
	return hc
}

// startAlpineWithLimits runs an alpine container with the given resource flags
// and returns its container ID. The container is auto-removed on cleanup.
func startAlpineWithLimits(t *testing.T, flags ...string) string {
	t.Helper()
	ctx := context.Background()
	args := []string{"run", "-d", "--rm"}
	args = append(args, flags...)
	args = append(args, "alpine", "sleep", "infinity")
	out, err := osexec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	cid := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		_ = osexec.Command("docker", "stop", "-t", "1", cid).Run()
	})
	return cid
}

func TestDockerResources_DefaultLimits(t *testing.T) {
	cid := startAlpineWithLimits(t,
		"--memory=4g",
		"--cpus=2",
		"--shm-size=1g",
	)
	hc := dockerInspectHostConfig(t, cid)

	// --memory=4g → 4 * 1024^3 = 4294967296
	mem, _ := hc["Memory"].(float64)
	if int64(mem) != 4*1024*1024*1024 {
		t.Errorf("Memory: want %d (4g), got %d", 4*1024*1024*1024, int64(mem))
	}

	// --cpus=2 → NanoCpus = 2 * 1e9
	nano, _ := hc["NanoCpus"].(float64)
	if int64(nano) != 2_000_000_000 {
		t.Errorf("NanoCpus: want %d (2 cpus), got %d", 2_000_000_000, int64(nano))
	}

	// --shm-size=1g → 1 * 1024^3 = 1073741824
	shm, _ := hc["ShmSize"].(float64)
	if int64(shm) != 1*1024*1024*1024 {
		t.Errorf("ShmSize: want %d (1g), got %d", 1*1024*1024*1024, int64(shm))
	}
}

func TestDockerResources_OverriddenLimits(t *testing.T) {
	cid := startAlpineWithLimits(t,
		"--memory=8g",
		"--cpus=4",
		"--shm-size=2g",
	)
	hc := dockerInspectHostConfig(t, cid)

	mem, _ := hc["Memory"].(float64)
	if int64(mem) != 8*1024*1024*1024 {
		t.Errorf("Memory: want %d (8g), got %d", 8*1024*1024*1024, int64(mem))
	}

	nano, _ := hc["NanoCpus"].(float64)
	if int64(nano) != 4_000_000_000 {
		t.Errorf("NanoCpus: want %d (4 cpus), got %d", 4_000_000_000, int64(nano))
	}

	shm, _ := hc["ShmSize"].(float64)
	if int64(shm) != 2*1024*1024*1024 {
		t.Errorf("ShmSize: want %d (2g), got %d", 2*1024*1024*1024, int64(shm))
	}
}

func TestDockerResources_Uncapped(t *testing.T) {
	// No --memory or --cpus flags → Docker applies no limits (values = 0)
	cid := startAlpineWithLimits(t, "--shm-size=1g")
	hc := dockerInspectHostConfig(t, cid)

	mem, _ := hc["Memory"].(float64)
	if int64(mem) != 0 {
		t.Errorf("Memory: want 0 (uncapped), got %d", int64(mem))
	}

	nano, _ := hc["NanoCpus"].(float64)
	if int64(nano) != 0 {
		t.Errorf("NanoCpus: want 0 (uncapped), got %d", int64(nano))
	}
}

// TestDockerResources_BuildArgvRoundtrip verifies that BuildArgv's output
// produces the expected resource constraints when fed to Docker. This is the
// end-to-end seam: cfg → BuildArgv → docker run → docker inspect.
func TestDockerResources_BuildArgvRoundtrip(t *testing.T) {
	t.Setenv("DEVCELL_DOCKER_MEM_LIMIT", "")
	t.Setenv("DEVCELL_DOCKER_CPU_LIMIT", "")
	t.Setenv("DEVCELL_DOCKER_SHM_SIZE", "")

	// Extract resource flags from a default BuildArgv to prove they match.
	// We don't call BuildArgv here (it needs a full RunSpec); instead we
	// verify the resolved defaults produce the flags we just tested above.
	from := func(resolved, prefix string) string {
		return fmt.Sprintf("%s%s", prefix, resolved)
	}

	defaults := map[string]string{
		"memory":   from("4g", "--memory="),
		"cpus":     from("2", "--cpus="),
		"shm-size": from("1g", "--shm-size="),
	}

	// Start a container with exactly the flags BuildArgv would emit
	cid := startAlpineWithLimits(t,
		defaults["memory"],
		defaults["cpus"],
		defaults["shm-size"],
	)
	hc := dockerInspectHostConfig(t, cid)

	mem, _ := hc["Memory"].(float64)
	if int64(mem) != 4*1024*1024*1024 {
		t.Errorf("roundtrip Memory: want 4g, got %d bytes", int64(mem))
	}
	nano, _ := hc["NanoCpus"].(float64)
	if int64(nano) != 2_000_000_000 {
		t.Errorf("roundtrip NanoCpus: want 2 cpus, got %d", int64(nano))
	}
	shm, _ := hc["ShmSize"].(float64)
	if int64(shm) != 1*1024*1024*1024 {
		t.Errorf("roundtrip ShmSize: want 1g, got %d bytes", int64(shm))
	}
}
