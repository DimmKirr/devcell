package main

import (
	"fmt"
	"os/exec"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/spf13/cobra"
)

var chromeCmd = &cobra.Command{
	Use:   "chrome [args...]",
	Short: "Open Chromium with a project-scoped profile",
	Long: `Opens Chromium with a project-scoped browser profile stored in the cell home.

Each project gets its own isolated Chrome profile so cookies, extensions, and
logins don't bleed across projects. All additional args are forwarded to
Chromium unchanged.

Examples:

    cell chrome
    cell chrome https://example.com`,
	DisableFlagParsing: true,
	RunE:               runChrome,
}

func runChrome(_ *cobra.Command, args []string) error {
	c, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	argv := buildChromeArgv(c.CellHome, args)
	cmd := exec.Command(argv[0], argv[1:]...)
	return cmd.Run()
}

func buildChromeArgv(cellHome string, extraArgs []string) []string {
	base := []string{
		"open", "-na", "Chromium",
		"--args",
		"--user-data-dir=" + cellHome + "/.chrome",
	}
	return append(base, extraArgs...)
}
