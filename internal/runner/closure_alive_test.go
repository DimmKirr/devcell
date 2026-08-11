package runner_test

import (
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// CELL-418: the probe argv must mount the nix volume at /nix and run inside
// the target image so the baked-in profile symlink can be followed.
func TestClosureAliveArgv_MountsVolumeAndImage(t *testing.T) {
	argv := runner.ClosureAliveArgv("devcell-nix-store", "devcell-user:base-thin")
	joined := strings.Join(argv, " ")

	if !strings.Contains(joined, "-v devcell-nix-store:/nix") {
		t.Errorf("expected nix volume mount, got: %s", joined)
	}
	if !strings.Contains(joined, "devcell-user:base-thin") {
		t.Errorf("expected image name in argv, got: %s", joined)
	}
	if !strings.Contains(joined, "--entrypoint") {
		t.Errorf("expected --entrypoint override, got: %s", joined)
	}
}

func TestClosureAliveArgv_ChecksProfileSymlink(t *testing.T) {
	argv := runner.ClosureAliveArgv("vol", "img:tag")
	script := argv[len(argv)-1]

	if !strings.Contains(script, runner.ProfilePath) {
		t.Errorf("script must check the profile path, got: %s", script)
	}
	if !strings.Contains(script, "readlink") {
		t.Errorf("script must readlink the profile, got: %s", script)
	}
}

func TestParseClosureAliveResult_Alive(t *testing.T) {
	path, alive := runner.ParseClosureAliveResult("/nix/store/abc123-user-environment\n", nil)
	if !alive {
		t.Error("expected alive=true for successful probe")
	}
	if path != "/nix/store/abc123-user-environment" {
		t.Errorf("expected parsed path, got: %q", path)
	}
}

func TestParseClosureAliveResult_Dead(t *testing.T) {
	path, alive := runner.ParseClosureAliveResult("", errStub("exit status 1"))
	if alive {
		t.Error("expected alive=false for failed probe")
	}
	if path != "" {
		t.Errorf("expected empty path on failure, got: %q", path)
	}
}

func TestParseClosureAliveResult_NilErrorEmptyOutput(t *testing.T) {
	_, alive := runner.ParseClosureAliveResult("", nil)
	if alive {
		t.Error("expected alive=false when output is empty even with nil error")
	}
}

// ClosureDeadWarning must produce a clear, actionable message when the
// closure is dead, and silence when alive.
func TestClosureDeadWarning_DeadProducesMessage(t *testing.T) {
	msg, dead := runner.ClosureDeadWarning(false)
	if !dead {
		t.Fatal("alive=false must produce a warning")
	}
	if !strings.Contains(msg, "garbage collected") {
		t.Errorf("warning must explain the closure was GC'd, got: %s", msg)
	}
	if !strings.Contains(msg, "Rebuild") {
		t.Errorf("warning must offer rebuild, got: %s", msg)
	}
}

func TestClosureDeadWarning_AliveIsSilent(t *testing.T) {
	_, dead := runner.ClosureDeadWarning(true)
	if dead {
		t.Error("alive=true must not warn")
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }
