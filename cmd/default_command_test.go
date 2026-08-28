package main

import (
	"reflect"
	"testing"
)

// rewriteDefaultCommand receives os.Args[1:] (everything after the binary
// name) and the resolved default command. It must inject the default command
// in front of user args so flags like `-c` reach the inner binary — the bug
// was `cell -c` dying at cobra flag parsing while `cell claude -c` worked.

func testKnownCmds() map[string]bool {
	return map[string]bool{
		"claude": true, "codex": true, "opencode": true, "gemini": true,
		"shell": true, "build": true, "init": true, "help": true,
	}
}

func TestRewriteDefaultCommand_ForwardsFlags(t *testing.T) {
	got := rewriteDefaultCommand([]string{"-c"}, "claude", testKnownCmds())
	want := []string{"claude", "-c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cell -c with default claude: got %v, want %v", got, want)
	}
}

func TestRewriteDefaultCommand_ForwardsPositionalArgs(t *testing.T) {
	got := rewriteDefaultCommand([]string{"--resume", "abc"}, "claude", testKnownCmds())
	want := []string{"claude", "--resume", "abc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRewriteDefaultCommand_BareInvocation(t *testing.T) {
	got := rewriteDefaultCommand(nil, "claude", testKnownCmds())
	want := []string{"claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bare cell with default claude: got %v, want %v", got, want)
	}
}

func TestRewriteDefaultCommand_NoDefaultUnchanged(t *testing.T) {
	got := rewriteDefaultCommand([]string{"-c"}, "", testKnownCmds())
	want := []string{"-c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("no default_command must leave args alone: got %v, want %v", got, want)
	}
}

func TestRewriteDefaultCommand_ExplicitSubcommandWins(t *testing.T) {
	got := rewriteDefaultCommand([]string{"build", "--stack", "go"}, "claude", testKnownCmds())
	want := []string{"build", "--stack", "go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("explicit subcommand must not be shadowed: got %v, want %v", got, want)
	}
}

func TestRewriteDefaultCommand_HelpAndVersionUntouched(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"--version"}, {"help"},
		{"completion", "zsh"}, {"__complete", "cl"}, {"__completeNoDesc", "cl"},
	} {
		got := rewriteDefaultCommand(args, "claude", testKnownCmds())
		if !reflect.DeepEqual(got, args) {
			t.Errorf("%v must bypass default command: got %v", args, got)
		}
	}
}
