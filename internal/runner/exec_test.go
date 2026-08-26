package runner_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

func TestBuildExecArgv_Basic(t *testing.T) {
	argv := runner.BuildExecArgv(runner.ExecSpec{
		ContainerName: "cell-foo-0-run",
		Binary:        "zsh",
		TTY:           true,
	})
	want := []string{"docker", "exec", "-it", "cell-foo-0-run", "zsh"}
	if len(argv) != len(want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}

func TestBuildExecArgv_WithArgs(t *testing.T) {
	argv := runner.BuildExecArgv(runner.ExecSpec{
		ContainerName: "cell-foo-0-run",
		Binary:        "ls",
		Args:          []string{"-la", "/workspace"},
		TTY:           true,
	})
	want := []string{"docker", "exec", "-it", "cell-foo-0-run", "ls", "-la", "/workspace"}
	if len(argv) != len(want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}

func TestBuildExecArgv_NoTTY(t *testing.T) {
	argv := runner.BuildExecArgv(runner.ExecSpec{
		ContainerName: "cell-foo-0-run",
		Binary:        "zsh",
		TTY:           false,
	})
	for _, a := range argv {
		if a == "-it" {
			t.Error("-it should not be present when TTY is false")
		}
	}
	want := []string{"docker", "exec", "cell-foo-0-run", "zsh"}
	if len(argv) != len(want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}
