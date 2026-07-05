package main

import (
	"context"
	"os"
	"time"

	authkube "github.com/DimmKirr/devcell/internal/auth/kube"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

var (
	authKubeOutput      string
	authKubeSAName      string
	authKubeNamespace   string
	authKubeTTL         time.Duration
	authKubeSkipCluster bool
	authKubeYes         bool
)

var authKubeCmd = &cobra.Command{
	Use:   "kube [source-kubeconfig]",
	Short: "Create a read-only kubeconfig from your admin context",
	Long: `Creates a ServiceAccount + view ClusterRoleBinding on the cluster,
mints a token, and writes a sibling kubeconfig (default: <source>-read) that
authenticates as the SA. Reads work; writes return 403 from the cluster.

Pair the output kubeconfig with a [[volumes]] mount in .devcell.toml so the
cell sees it; the kubernetes-mcp-server and your own kubectl inside the cell
will both use it transparently.

Requires kubectl on the host PATH.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var source string
		if len(args) == 1 {
			source = args[0]
		}

		// Wire the prompt: tests inject directly; here we route through ux.
		authkube.SetConfirmFn(func(_ context.Context, msg string) (bool, error) {
			return ux.GetConfirmation(msg)
		})

		return authkube.Bootstrap(cmd.Context(), authkube.Options{
			Source:      source,
			Output:      authKubeOutput,
			SAName:      authKubeSAName,
			Namespace:   authKubeNamespace,
			TTL:         authKubeTTL,
			SkipCluster: authKubeSkipCluster,
			Yes:         authKubeYes,
		}, os.Stdout)
	},
}

func init() {
	authKubeCmd.Flags().StringVar(&authKubeOutput, "output", "", "output kubeconfig path (default: <source>-read)")
	authKubeCmd.Flags().StringVar(&authKubeSAName, "sa-name", "", "ServiceAccount name on the cluster (default: $USER-readonly)")
	authKubeCmd.Flags().StringVar(&authKubeNamespace, "namespace", "default", "ServiceAccount namespace")
	authKubeCmd.Flags().DurationVar(&authKubeTTL, "ttl", 0, "token duration request (default: 8760h; cluster may cap)")
	authKubeCmd.Flags().BoolVar(&authKubeSkipCluster, "skip-cluster", false, "skip cluster mutation; only produce the kubeconfig")
	authKubeCmd.Flags().BoolVar(&authKubeYes, "yes", false, "skip the interactive confirmation prompt")

	authCmd.AddCommand(authKubeCmd)
}
