package scaffold_test

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/scaffold"
)

// shortSHA returns the abbreviated commit hash of HEAD.
func shortSHA() string {
	cmd := osexec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("dev%s", time.Now().Format("150405"))
	}
	return strings.TrimSpace(string(out))
}

// TestGenerateTestdata writes generated flake.nix and Dockerfile variants to
// test/results/<YYYYMMDD-HHMMSS>-<sha>/generate-testdata/ for manual and LLM-assisted review.
// Run with: go test ./internal/scaffold/ -run TestGenerateTestdata -v
func TestGenerateTestdata(t *testing.T) {
	ts := time.Now().Format("20060102-150405")
	runDir := filepath.Join("..", "..", "test", "results", fmt.Sprintf("%s-%s", ts, shortSHA()))
	baseDir := filepath.Join(runDir, "generate-testdata")

	cases := []struct {
		name        string
		stack       string
		modules     []string
		version     string
		nixhome     string
		baseImage   string
		withNixhome bool
	}{
		{
			name:    "default-ultimate",
			stack:   "ultimate",
			modules: nil,
			version: "v1.0.0",
		},
		{
			name:    "go-only",
			stack:   "go",
			modules: nil,
			version: "v1.0.0",
		},
		{
			name:    "base-plus-go",
			stack:   "base",
			modules: []string{"go"},
			version: "v1.0.0",
		},
		{
			name:    "base-plus-go-electronics-desktop",
			stack:   "base",
			modules: []string{"go", "electronics", "desktop"},
			version: "v2.3.4",
		},
		{
			name:    "python-plus-infra",
			stack:   "python",
			modules: []string{"infra", "build"},
			version: "v1.0.0",
		},
		{
			name:    "fullstack-no-modules",
			stack:   "fullstack",
			modules: nil,
			version: "v1.0.0",
		},
		{
			name:        "go-with-nixhome-path",
			stack:       "go",
			modules:     []string{"electronics"},
			version:     "v1.0.0",
			nixhome:     "/Users/dmitry/dev/dimmkirr/devcell/nixhome",
			withNixhome: true,
		},
		{
			name:      "custom-base-image",
			stack:     "node",
			modules:   []string{"python"},
			version:   "v1.0.0",
			baseImage: "myregistry.io/devcell:custom-v42",
		},
		{
			name:    "electronics-standalone",
			stack:   "electronics",
			modules: nil,
			version: "v1.0.0",
		},
		{
			name:    "base-many-modules",
			stack:   "base",
			modules: []string{"go", "node", "python", "infra", "build", "electronics", "desktop"},
			version: "v1.0.0",
		},
	}

	for _, tc := range cases {
		dir := filepath.Join(baseDir, tc.name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}

		// Generate flake.nix
		flake := scaffold.GenerateFlakeNix(tc.stack, tc.modules, tc.version, tc.withNixhome)
		if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(flake), 0644); err != nil {
			t.Fatalf("write flake.nix: %v", err)
		}

		// Generate Dockerfile
		var dockerfile string
		if tc.withNixhome {
			dockerfile = scaffold.GenerateDockerfileWithNixhome(tc.baseImage, true, tc.stack, tc.modules)
		} else {
			dockerfile = scaffold.GenerateDockerfileWithNixhome(tc.baseImage, false, tc.stack, tc.modules)
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
			t.Fatalf("write Dockerfile: %v", err)
		}

		t.Logf("wrote %s/", tc.name)
	}

	t.Logf("testdata written to: %s", baseDir)
}
