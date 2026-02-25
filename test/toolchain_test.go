package container_test

// toolchain_test.go — language runtime and dev tool availability tests.

import (
	"strings"
	"testing"
)

// TestToolchain — key dev tools must be on PATH for the session user.
func TestToolchain(t *testing.T) {
	c := startEnvContainer(t)

	tools := []struct {
		bin        string
		versionCmd string
	}{
		{"go", "go version"},
		{"node", "node --version"},
		{"terraform", "terraform version"},
		{"tofu", "tofu version"},
		{"home-manager", "home-manager --version"},
	}

	for _, tool := range tools {
		out, code := asUser(t, c, tool.versionCmd)
		if code != 0 {
			t.Errorf("FAIL %s: not available (exit %d): %s", tool.bin, code, out)
		} else {
			t.Logf("PASS %s: %s", tool.bin, strings.SplitN(out, "\n", 2)[0])
		}
	}
}
