package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/runner"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var shellCmd = &cobra.Command{
	Use:   "shell [-- command [args...]]",
	Short: "Open an interactive shell in a devcell container",
	Long: `Opens an interactive zsh shell inside a devcell container.

If a container is already running (from 'cell start'), attaches to it.
Otherwise starts a new container. The current working directory is mounted
as /workspace. Optionally pass a command after -- to run it
non-interactively instead of starting a shell.

Examples:

    cell shell
    cell shell -- ls /workspace`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		applyOutputFlags()

		c, err := config.LoadFromOS()
		if err == nil && runner.ContainerRunning(context.Background(), c.ContainerName) {
			binary := "zsh"
			var execArgs []string
			for i, a := range args {
				if a == "--" {
					rest := args[i+1:]
					if len(rest) > 0 {
						binary = rest[0]
						execArgs = rest[1:]
					}
					break
				}
			}
			return execIntoContainer(c.ContainerName, binary, execArgs)
		}

		// No running container: fall through to docker run.
		for i, a := range args {
			if a == "--" {
				rest := args[i+1:]
				cellFlags := args[:i]
				if len(rest) > 0 {
					binary := rest[0]
					userArgs := make([]string, 0, len(cellFlags)+len(rest)-1)
					userArgs = append(userArgs, cellFlags...)
					userArgs = append(userArgs, rest[1:]...)
					return runAgent(binary, nil, userArgs, nil)
				}
				return runAgent("zsh", nil, cellFlags, nil)
			}
		}
		return runAgent("zsh", nil, args, nil)
	},
}

func execIntoContainer(containerName, binary string, args []string) error {
	spec := runner.ExecSpec{
		ContainerName: containerName,
		Binary:        binary,
		Args:          args,
		TTY:           isatty.IsTerminal(os.Stdin.Fd()),
	}
	argv := runner.BuildExecArgv(spec)

	if scanFlag("--dry-run") {
		fmt.Println(shellJoin(argv))
		return nil
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec into %s: %w", containerName, err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			_ = cmd.Process.Signal(sig)
		}
	}()

	waitErr := cmd.Wait()
	signal.Stop(sigCh)
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return waitErr
	}
	return nil
}
