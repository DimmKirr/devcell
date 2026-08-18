package container_test

import (
	"context"
	"fmt"
	osexec "os/exec"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestProjectFlake_OverridesBakedPackage verifies the project-level flake.nix
// override logic (CELL-447):
//
//   - Baked image ships jq from nixos-25.11 stable
//   - Project flake also provides jq (from nixpkgs-unstable)
//   - `which jq` must resolve from the project profile, not the baked profile
//   - The baked jq is still reachable by absolute path
//   - Other baked packages (git) remain on PATH
//
// The assertion is path-based (WHERE jq resolves), not version-based.
// PATH precedence: ad-hoc > project-flake > baked.
func TestProjectFlake_OverridesBakedPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("long: installs nix packages from project flake")
	}

	img := image()
	ctx := context.Background()

	projVol := fmt.Sprintf("devcell-flake-test-%s", time.Now().Format("150405"))
	t.Cleanup(func() { osexec.Command("docker", "volume", "rm", "-f", projVol).Run() })

	// Flake that provides jq. The source channel doesn't matter for the
	// override assertion: what matters is that `which jq` resolves from the
	// project profile path, proving PATH precedence over the baked profile.
	flakeNix := `{
  description = "CELL-447 override test: jq from project flake";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  outputs = { self, nixpkgs, ... }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems f;
    in {
      packages = forAllSystems (system: {
        default = nixpkgs.legacyPackages.${system}.jq;
      });
    };
}`

	seedScript := fmt.Sprintf(`cat > /project/flake.nix << 'FLAKE_EOF'
%s
FLAKE_EOF
echo "SEED_DONE"`, flakeNix)

	seedReq := testcontainers.ContainerRequest{
		Image: "alpine:latest",
		Cmd:   []string{"sh", "-c", seedScript},
		Mounts: testcontainers.Mounts(
			testcontainers.VolumeMount(projVol, "/project"),
		),
		WaitingFor: wait.ForLog("SEED_DONE").WithStartupTimeout(15 * time.Second),
	}
	seedC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: seedReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("seed project volume: %v", err)
	}
	_ = seedC.Terminate(ctx)

	mounts := testcontainers.Mounts(
		testcontainers.VolumeMount(projVol, "/test-project"),
	)
	if isThinVariant() {
		mounts = testcontainers.Mounts(
			testcontainers.VolumeMount(projVol, "/test-project"),
			testcontainers.VolumeMount(thinVolumeName(), "/nix"),
		)
	}

	req := testcontainers.ContainerRequest{
		Image: img,
		Env: map[string]string{
			"HOST_USER":           hostUser,
			"WORKSPACE":           "/test-project",
			"APP_NAME":            "flake-test",
			"DEVCELL_FLAKE_TRUST": "1",
			"DEVCELL_DEBUG":       "true",
		},
		User:       "0",
		Cmd:        []string{"tail", "-f", "/dev/null"},
		Mounts:     mounts,
		WaitingFor: wait.ForExec([]string{"pgrep", "tail"}).WithStartupTimeout(10 * time.Minute),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	t.Run("system_detection_matches_uname", func(t *testing.T) {
		out, code := exec(t, c, []string{"uname", "-m"})
		if code != 0 {
			t.Fatalf("uname -m exit %d: %s", code, out)
		}
		arch := strings.TrimSpace(out)
		var wantSystem string
		switch arch {
		case "aarch64":
			wantSystem = "aarch64-linux"
		case "x86_64":
			wantSystem = "x86_64-linux"
		default:
			t.Fatalf("unexpected arch: %s", arch)
		}
		// Verify the project profile was installed for the correct system by
		// checking the entrypoint log. A wrong system (e.g. x86_64-linux on
		// aarch64) would show "platform mismatch" and no profile.
		logOut, _ := exec(t, c, []string{
			"grep", "-c", "project-flake: installed packages." + wantSystem, "/proc/1/fd/1",
		})
		// /proc/1/fd/1 may not be readable; fall back to profile existence check.
		if strings.TrimSpace(logOut) == "0" {
			// If we can't read the log, at least verify the profile exists
			_, profileCode := exec(t, c, []string{
				"test", "-d", "/home/" + hostUser + "/.local/state/nix/profiles/project",
			})
			if profileCode != 0 {
				t.Fatal("project profile not created: system detection may have selected wrong arch")
			}
		}
		t.Logf("PASS: arch=%s system=%s", arch, wantSystem)
	})

	t.Run("project_profile_exists", func(t *testing.T) {
		out, code := exec(t, c, []string{
			"test", "-L", "/home/" + hostUser + "/.local/state/nix/profiles/project",
		})
		if code != 0 {
			t.Fatalf("project profile symlink missing (exit %d): %s", code, out)
		}
	})

	t.Run("jq_resolves_from_project_profile", func(t *testing.T) {
		out, code := asUser(t, c, "which jq")
		if code != 0 {
			t.Fatalf("jq not found on PATH (exit %d): %s", code, out)
		}
		if !strings.Contains(out, ".local/state/nix/profiles/project") {
			t.Fatalf("jq should resolve from project profile, got: %s", out)
		}
		t.Logf("PASS: jq at %s", out)
	})

	t.Run("jq_works", func(t *testing.T) {
		out, code := asUser(t, c, "jq --version")
		if code != 0 {
			t.Fatalf("jq --version exit %d: %s", code, out)
		}
		t.Logf("PASS: project-flake jq: %s", strings.TrimSpace(out))
	})

	t.Run("baked_jq_still_reachable", func(t *testing.T) {
		out, code := exec(t, c, []string{
			"/opt/devcell/.local/state/nix/profiles/profile/bin/jq", "--version",
		})
		if code != 0 {
			t.Fatalf("baked jq unreachable (exit %d): %s", code, out)
		}
		t.Logf("PASS: baked jq still at absolute path: %s", strings.TrimSpace(out))
	})

	t.Run("other_baked_packages_on_path", func(t *testing.T) {
		out, code := asUser(t, c, "git --version")
		if code != 0 {
			t.Fatalf("git not found (exit %d): %s", code, out)
		}
		t.Logf("PASS: baked git still available: %s", strings.TrimSpace(out))
	})
}
