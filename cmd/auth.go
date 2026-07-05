package main

import "github.com/spf13/cobra"

// authCmd is the umbrella for host-side credential bootstrap subcommands
// (kube today; aws/gcp/github/chrome to follow). Each subcommand produces
// credentials on the host that the cell mounts via `[[volumes]]` and reads
// via `[env]` — devcell itself never sees admin secrets.
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Bootstrap host-side credentials for the cell to mount",
	Long: `Host-side credential bootstrap. Each subcommand prepares a scoped
credential on your host that the cell can mount read-only.

Currently available:
  cell auth kube     bootstrap a read-only kubeconfig from your admin context`,
}
