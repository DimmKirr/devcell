package main_test

import (
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

func TestShellExecArgv_DefaultZsh(t *testing.T) {
	argv := runner.BuildExecArgv(runner.ExecSpec{
		ContainerName: "cell-myproject-0-run",
		Binary:        "zsh",
		TTY:           true,
	})
	want := []string{"docker", "exec", "-it", "cell-myproject-0-run", "zsh"}
	if len(argv) != len(want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}

func TestShellExecArgv_CustomCommand(t *testing.T) {
	argv := runner.BuildExecArgv(runner.ExecSpec{
		ContainerName: "cell-myproject-0-run",
		Binary:        "bash",
		Args:          []string{"-c", "echo hello"},
		TTY:           false,
	})
	want := []string{"docker", "exec", "cell-myproject-0-run", "bash", "-c", "echo hello"}
	if len(argv) != len(want) {
		t.Fatalf("got %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}
