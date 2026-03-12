package container_test

// helpers — shared test infrastructure: image building, container lifecycle, exec.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	ultimateOnce sync.Once
	ultimateTag  string
	ultimateErr  error

	baseOnce sync.Once
	baseTag  string
	baseErr  error

	electronicsOnce sync.Once
	electronicsTag  string
	electronicsErr  error
)

// TestMain cleans up locally-built test images after all tests complete.
func TestMain(m *testing.M) {
	code := m.Run()
	if ultimateTag != "" {
		osexec.Command("docker", "rmi", ultimateTag).Run()
	}
	if baseTag != "" {
		osexec.Command("docker", "rmi", baseTag).Run()
	}
	if electronicsTag != "" {
		osexec.Command("docker", "rmi", electronicsTag).Run()
	}
	os.Exit(code)
}

// shortSHA returns the abbreviated commit hash of HEAD.
func shortSHA() string {
	out, err := osexec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		panic(fmt.Sprintf("git rev-parse: %v", err))
	}
	return strings.TrimSpace(string(out))
}

// buildLocalImage builds a bake target with a unique tag and returns the tag.
func buildLocalImage(target, tagPrefix string) (string, error) {
	tag := fmt.Sprintf("%s:%s-%s", tagPrefix, shortSHA(), time.Now().Format("20060102T150405"))
	log.Printf("Building %s image: %s", target, tag)
	cmd := osexec.Command("docker", "buildx", "bake",
		"--file", "docker-bake.hcl",
		"--load",
		"--set", fmt.Sprintf("%s.tags=%s", target, tag),
		target)
	cmd.Dir = ".."
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build %s: %w", target, err)
	}
	return tag, nil
}

// image returns the ultimate image tag for tests.
// Uses DEVCELL_TEST_IMAGE if set (CI); otherwise builds local-ultimate once with a unique tag.
func image() string {
	if img := os.Getenv("DEVCELL_TEST_IMAGE"); img != "" {
		return img
	}
	ultimateOnce.Do(func() {
		ultimateTag, ultimateErr = buildLocalImage("local-ultimate", "devcell-test")
	})
	if ultimateErr != nil {
		panic(fmt.Sprintf("image: %v", ultimateErr))
	}
	return ultimateTag
}

// baseImage returns the base image tag for entrypoint tests.
// Uses DEVCELL_TEST_BASE_IMAGE if set (CI); otherwise builds local-base once with a unique tag.
func baseImage() string {
	if img := os.Getenv("DEVCELL_TEST_BASE_IMAGE"); img != "" {
		return img
	}
	baseOnce.Do(func() {
		baseTag, baseErr = buildLocalImage("local-base", "devcell-test-base")
	})
	if baseErr != nil {
		panic(fmt.Sprintf("baseImage: %v", baseErr))
	}
	return baseTag
}

// ── Electronics image (base + home-manager switch devcell-electronics) ────────
//
// Builds a user-level image following the scaffold Dockerfile pattern:
//   1. FROM base image (nix + home-manager, no profile)
//   2. Copy local nixhome/ flake
//   3. home-manager switch --flake .#devcell-electronics (smallest profile with desktop module)
//   4. npm install patchright-mcp (provides mcp-server-patchright binary)
//
// Used by stealth MCP tests instead of the pre-built ultimate image.

const elecDockerfile = `FROM {{BASE_IMAGE}}

COPY --chown=devcell:usergroup nixhome/ /opt/devcell/.config/devcell/nixhome/
COPY --chown=devcell:usergroup flake.nix /opt/devcell/.config/devcell/

RUN ARCH=$(uname -m) && \
    [ "$ARCH" = "aarch64" ] && ARCH_SUFFIX="-aarch64" || ARCH_SUFFIX="" && \
    home-manager switch \
      --flake "/opt/devcell/.config/devcell#devcell-electronics${ARCH_SUFFIX}" \
      --impure && \
    ln -sfT "$(readlink -f /opt/devcell/.nix-profile)" \
            /nix/var/nix/profiles/per-user/devcell/profile

COPY --chown=devcell:usergroup package.json /opt/npm-tools/
RUN cd /opt/npm-tools && npm install
ENV PATH="/opt/npm-tools/node_modules/.bin:${PATH}"
`

const elecFlakeNix = `{
  description = "DevCell electronics test profile";
  inputs.devcell.url = "path:./nixhome";
  outputs = { self, devcell, ... }: {
    homeConfigurations = devcell.homeConfigurations;
  };
}
`

const elecPackageJSON = `{
  "name": "devcell-tools",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "patchright-mcp": "^0.0.68"
  }
}
`

// buildElectronicsImage creates a temp build context with the local nixhome,
// writes a Dockerfile targeting devcell-electronics, and runs docker build.
func buildElectronicsImage() (string, error) {
	baseImg := baseImage()

	dir, err := os.MkdirTemp("", "devcell-elec-test-*")
	if err != nil {
		return "", fmt.Errorf("mkdtemp: %w", err)
	}
	defer os.RemoveAll(dir)

	// Write Dockerfile with base image substituted.
	dockerfile := strings.ReplaceAll(elecDockerfile, "{{BASE_IMAGE}}", baseImg)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return "", fmt.Errorf("write Dockerfile: %w", err)
	}

	// Write flake.nix (path:./nixhome input).
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(elecFlakeNix), 0644); err != nil {
		return "", fmt.Errorf("write flake.nix: %w", err)
	}

	// Write package.json (only patchright-mcp).
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(elecPackageJSON), 0644); err != nil {
		return "", fmt.Errorf("write package.json: %w", err)
	}

	// Copy local nixhome/ into the build context.
	nixhomeSrc := filepath.Join("..", "nixhome")
	nixhomeDst := filepath.Join(dir, "nixhome")
	if err := copyDirRecursive(nixhomeSrc, nixhomeDst); err != nil {
		return "", fmt.Errorf("copy nixhome: %w", err)
	}

	tag := fmt.Sprintf("devcell-test-electronics:%s-%s", shortSHA(), time.Now().Format("20060102T150405"))
	log.Printf("Building electronics image: %s (from base %s)", tag, baseImg)
	cmd := osexec.Command("docker", "build", "--no-cache", "--progress=plain", "-t", tag, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build electronics: %w", err)
	}
	return tag, nil
}

// copyDirRecursive copies src directory tree to dst.
func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// electronicsImage returns the electronics image tag.
// Uses DEVCELL_TEST_ELECTRONICS_IMAGE if set (CI); otherwise builds once from
// base + local nixhome with devcell-electronics profile.
func electronicsImage() string {
	if img := os.Getenv("DEVCELL_TEST_ELECTRONICS_IMAGE"); img != "" {
		return img
	}
	electronicsOnce.Do(func() {
		electronicsTag, electronicsErr = buildElectronicsImage()
	})
	if electronicsErr != nil {
		panic(fmt.Sprintf("electronicsImage: %v", electronicsErr))
	}
	return electronicsTag
}

// startElectronicsContainer starts a container from the electronics image.
func startElectronicsContainer(t *testing.T, env map[string]string) testcontainers.Container {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: electronicsImage(),
		Env:   env,
		User:  "0",
		Cmd:   []string{"tail", "-f", "/dev/null"},
		WaitingFor: wait.ForExec([]string{"pgrep", "tail"}).
			WithStartupTimeout(30 * 1e9),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start electronics container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	return c
}

// startElectronicsEnvContainer starts an electronics container with standard env.
func startElectronicsEnvContainer(t *testing.T) testcontainers.Container {
	t.Helper()
	return startElectronicsContainer(t, map[string]string{
		"HOST_USER": hostUser,
		"APP_NAME":  "test",
	})
}

func startContainer(t *testing.T, env map[string]string) testcontainers.Container {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image: image(),
		Env:   env,
		User:  "0", // entrypoint.sh starts as root, drops via gosu
		Cmd:   []string{"tail", "-f", "/dev/null"},
		WaitingFor: wait.ForExec([]string{"pgrep", "tail"}).
			WithStartupTimeout(30 * 1e9),
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	return c
}

func exec(t *testing.T, c testcontainers.Container, cmd []string) (string, int) {
	t.Helper()
	ctx := context.Background()
	code, reader, err := c.Exec(ctx, cmd)
	if err != nil {
		t.Fatalf("exec %v: %v", cmd, err)
	}
	var stdout bytes.Buffer
	stdcopy.StdCopy(&stdout, io.Discard, reader)
	return strings.TrimSpace(stdout.String()), code
}
