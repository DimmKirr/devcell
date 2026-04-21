package ux

import (
	"fmt"
	"strings"
)

// BuildErrorHint describes a user-facing explanation and fix for a known build failure.
type BuildErrorHint struct {
	Title string
	Body  string
	Fixes []string
}

// buildErrorPatterns maps output substrings to user-facing hints.
// Patterns are checked case-insensitively in order; first match wins.
var buildErrorPatterns = []struct {
	needle string
	hint   BuildErrorHint
}{
	{
		needle: "no space left on device",
		hint: BuildErrorHint{
			Title: "No space left on device",
			Body:  "Docker ran out of disk space during the build.",
			Fixes: []string{
				"docker buildx prune -af          # safe — clears build cache only",
				"docker image prune               # run after stopping old devcell containers",
			},
		},
	},
	{
		needle: "failed to fetch",
		hint: BuildErrorHint{
			Title: "Nix fetch failed",
			Body:  "A nix package could not be downloaded. This is usually a network issue.",
			Fixes: []string{
				"cell build                       # retry — may be a transient failure",
				"cell build --update              # refresh flake inputs and rebuild",
			},
		},
	},
	{
		needle: "dial tcp",
		hint: BuildErrorHint{
			Title: "Network error during build",
			Body:  "Docker could not reach a remote host. Check your internet connection.",
			Fixes: []string{
				"cell build                       # retry after checking connectivity",
			},
		},
	},
	{
		needle: "error: attribute",
		hint: BuildErrorHint{
			Title: "Nix attribute error",
			Body:  "A package attribute in your nixhome config does not exist in nixpkgs.",
			Fixes: []string{
				"task nix:validate                # check nix syntax and attribute names",
			},
		},
	},
	{
		needle: "error: undefined variable",
		hint: BuildErrorHint{
			Title: "Nix undefined variable",
			Body:  "Your nixhome config references a variable that is not in scope.",
			Fixes: []string{
				"task nix:validate                # check nix syntax",
			},
		},
	},
	{
		needle: "dockerfile parse error",
		hint: BuildErrorHint{
			Title: "Dockerfile syntax error",
			Body:  "The generated Dockerfile contains a syntax error.",
			Fixes: []string{
				"cell build                       # retry after checking .devcell/Dockerfile",
			},
		},
	},
	{
		needle: "permission denied",
		hint: BuildErrorHint{
			Title: "Permission denied",
			Body:  "A file or directory could not be accessed during the build.",
			Fixes: []string{
				"ls -la .devcell/                 # inspect build context permissions",
			},
		},
	},
	{
		needle: "cannot connect to the docker daemon",
		hint: BuildErrorHint{
			Title: "Docker daemon is not running",
			Body:  "Could not connect to the Docker daemon. Docker Desktop may not be started.",
			Fixes: []string{
				"open -a Docker                   # start Docker Desktop (macOS)",
				"                                 # or start Docker Desktop from the menu bar",
			},
		},
	},
	// Fallback: generic docker build failure — must be last.
	{
		needle: "docker build: exit status",
		hint: BuildErrorHint{
			Title: "Docker build failed",
			Body:  "The docker build command exited with an error. Run with --debug to see the full output.",
			Fixes: []string{
				"cell build --debug               # stream full build log",
			},
		},
	},
}

// ClassifyBuildOutput scans docker build output for known error patterns and
// returns a user-facing hint. Returns nil when no pattern matches.
func ClassifyBuildOutput(output string) *BuildErrorHint {
	lower := strings.ToLower(output)
	for _, p := range buildErrorPatterns {
		if strings.Contains(lower, p.needle) {
			h := p.hint
			return &h
		}
	}
	return nil
}

// PrintBuildErrorHint renders a user-facing error panel for a build failure hint.
func PrintBuildErrorHint(hint *BuildErrorHint) {
	border := StyleError.Render("─────────────────────────────────────────")
	fmt.Println()
	fmt.Printf(" %s\n", border)
	fmt.Printf(" %s  %s\n", StyleError.Render("✗"), StyleBold.Render(hint.Title))
	fmt.Printf(" %s\n", StyleMuted.Render(hint.Body))
	if len(hint.Fixes) > 0 {
		fmt.Println()
		fmt.Printf(" %s\n", StyleBold.Render("To fix:"))
		for _, fix := range hint.Fixes {
			fmt.Printf("   %s %s\n", StyleAccent.Render("•"), StyleInfo.Render(fix))
		}
	}
	fmt.Printf(" %s\n", border)
	fmt.Println()
}
