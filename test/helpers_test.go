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
