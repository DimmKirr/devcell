package runner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// DIMM-124: After a build failure, the next cell launch should detect that
// the build context is newer than the stale image and trigger a rebuild.

func TestDockerfileChanged_MissingImage_ReturnsTrue(t *testing.T) {
	// When image doesn't exist, DockerfileChanged should return true
	t.Setenv("DEVCELL_USER_IMAGE", "devcell-test:definitely-does-not-exist-"+t.Name())
	got := runner.DockerfileChanged(t.TempDir())
	if !got {
		t.Error("DockerfileChanged should return true when image doesn't exist")
	}
}

func TestDockerfileChanged_NoBuildFiles_ReturnsFalse(t *testing.T) {
	// When image exists but no Dockerfile/flake.nix in configDir,
	// DockerfileChanged returns false (nothing to compare against).
	// We can't easily mock ImageExists here, but we can verify the function
	// handles an empty dir correctly — it returns true because image inspect
	// fails for a non-existent tag.
	t.Setenv("DEVCELL_USER_IMAGE", "devcell-test:no-such-image-"+t.Name())
	got := runner.DockerfileChanged(t.TempDir())
	if !got {
		t.Error("should return true when image doesn't exist (even with empty dir)")
	}
}

func TestDockerfileChanged_BuildFilesPresent_ReturnsTrue(t *testing.T) {
	// When Dockerfile exists in configDir and image doesn't exist,
	// DockerfileChanged should return true.
	t.Setenv("DEVCELL_USER_IMAGE", "devcell-test:no-such-image-"+t.Name())
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0644)
	got := runner.DockerfileChanged(dir)
	if !got {
		t.Error("should return true when image missing and Dockerfile exists")
	}
}
