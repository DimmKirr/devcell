package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/DimmKirr/devcell/internal/serve"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start HTTP API server for LLM commands",
	Long: `Starts an OpenAI-compatible HTTP server that proxies chat completions
to LLM agent binaries (claude, opencode).

Endpoints:

    POST /v1/chat/completions  — OpenAI chat completions API
    GET  /api/v1/health        — health check

The model field selects the agent: "claude", "opencode", or
"claude/claude-sonnet-4-5" (agent/submodel).

Request:

    {"model": "claude", "messages": [{"role": "user", "content": "explain this"}]}

Examples:

    cell serve
    cell serve --port 9090`,
	RunE: runServe,
}

var servePort int

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", serve.DefaultPort, "port to listen on")
}

func runServe(cmd *cobra.Command, args []string) error {
	apiKey := os.Getenv("DEVCELL_API_KEY")
	if apiKey == "" {
		apiKey = serve.GenerateAPIKey()
	}

	if scanFlag("--dry-run") {
		fmt.Printf("serve: port=%d api_key=%s\n", servePort, apiKey)
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	exec := &serve.ShellExecutor{}
	srv := serve.NewServer(exec, servePort)
	srv.SetAPIKey(apiKey)

	addr, errCh := srv.Start(ctx)
	if addr == "" {
		return <-errCh
	}

	fmt.Fprintf(os.Stderr, "devcell serve listening on %s\n", addr)
	fmt.Fprintf(os.Stderr, "API key: %s\n", apiKey)

	return <-errCh
}
