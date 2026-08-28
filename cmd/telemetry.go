package main

import (
	"fmt"
	"os"

	"github.com/DimmKirr/devcell/internal/config"
	"github.com/DimmKirr/devcell/internal/telemetry"
	"github.com/spf13/cobra"
)

var telemetryCmd = &cobra.Command{
	Use:   "telemetry",
	Short: "Manage anonymous usage analytics",
	Long: `Manage opt-in anonymous usage analytics.

devcell collects anonymous feature-usage data (which commands, engines, and
stacks are popular) to guide development priorities. No personal data, file
paths, or command arguments are ever sent.

  cell telemetry          show current status
  cell telemetry on       opt in  (generates an anonymous ID)
  cell telemetry off      opt out (preserves ID for re-enable)

Respects DO_NOT_TRACK=1 (consoledonottrack.com).`,
	RunE: telemetryStatusCmd.RunE,
}

var telemetryOnCmd = &cobra.Command{
	Use:   "on",
	Short: "Enable anonymous usage analytics",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir := resolveConfigDir()
		cfg, err := telemetry.Enable(configDir)
		if err != nil {
			return fmt.Errorf("enable telemetry: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Telemetry enabled.\nAnonymous ID: %s\n", cfg.AnonymousID)
		return nil
	},
}

var telemetryOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Disable anonymous usage analytics",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir := resolveConfigDir()
		if _, err := telemetry.Disable(configDir); err != nil {
			return fmt.Errorf("disable telemetry: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Telemetry disabled.")
		return nil
	},
}

var telemetryStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show telemetry status",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir := resolveConfigDir()
		cfg := telemetry.LoadConfig(configDir)

		w := cmd.OutOrStdout()
		if os.Getenv("DO_NOT_TRACK") == "1" {
			fmt.Fprintln(w, "Telemetry: disabled (DO_NOT_TRACK=1 is set)")
		} else if cfg.Enabled {
			fmt.Fprintf(w, "Telemetry: enabled\nAnonymous ID: %s\n", cfg.AnonymousID)
		} else {
			fmt.Fprintln(w, "Telemetry: disabled")
		}
		return nil
	},
}

func resolveConfigDir() string {
	if c, err := config.LoadFromOS(); err == nil {
		return c.ConfigDir
	}
	home, _ := os.UserHomeDir()
	return home + "/.config/devcell"
}

func init() {
	telemetryCmd.AddCommand(telemetryOnCmd, telemetryOffCmd, telemetryStatusCmd)
}
