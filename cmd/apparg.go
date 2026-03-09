package main

import (
	"bytes"
	"os/exec"
	"sort"
	"strings"

	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

// resolveAppArg resolves a positional app argument to a full AppName.
// Accepts either a full name ("devcell-271") or a numeric suffix ("271").
// A bare suffix is expanded by scanning running cell-* containers.
func resolveAppArg(arg string) string {
	// If it looks like a full app name (contains a dash), use as-is.
	if strings.Contains(arg, "-") {
		return arg
	}
	// Treat as suffix — scan running containers for a match.
	names := runningAppNames()
	for _, name := range names {
		if strings.HasSuffix(name, "-"+arg) {
			return name
		}
	}
	// No match found — return as-is and let the caller's docker inspect fail
	// with a clear "container not found" error.
	return arg
}

// runningAppNames returns AppNames of running cell containers by parsing
// container names of the form "cell-<appname>-run".
func runningAppNames() []string {
	out, err := exec.Command("docker", "ps",
		"--filter", "name=cell-",
		"--format", "{{.Names}}").Output()
	if err != nil {
		return nil
	}
	return parseContainerNames(string(out))
}

// parseContainerNames extracts AppNames from docker ps output lines.
// Each line should be a container name like "cell-devcell-271-run".
func parseContainerNames(output string) []string {
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "cell-") && strings.HasSuffix(line, "-run") {
			appName := line[len("cell-") : len(line)-len("-run")]
			names = append(names, appName)
		}
	}
	return names
}

// selectCell shows an interactive picker when multiple cells are running.
// Returns the selected AppName.
func selectCell(apps map[string]string) (string, error) {
	var names []string
	for name := range apps {
		names = append(names, name)
	}
	sort.Strings(names)
	return ux.GetSelection("Multiple cells found — select one", names)
}

// completeRunningApps provides shell completion for running cell container names.
func completeRunningApps(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	out, err := exec.Command("docker", "ps",
		"--filter", "name=cell-",
		"--format", "{{.Names}}").Output()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []string
	for _, line := range strings.Split(string(bytes.TrimSpace(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "cell-") && strings.HasSuffix(line, "-run") {
			appName := line[len("cell-") : len(line)-len("-run")]
			completions = append(completions, appName)
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
