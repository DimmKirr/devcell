package serve_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/serve"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// TestPTYServe_HTTP_RealClaude starts cell serve with a PTYExecutor backed by
// the real claude binary, then sends an HTTP request via the OpenAI SDK and
// verifies the response round-trips through the server.
func TestPTYServe_HTTP_RealClaude(t *testing.T) {
	if testing.Short() {
		t.Skip("long: requires authenticated claude binary")
	}

	claudeBin := "/opt/devcell/.local/state/nix/profiles/profile/bin/claude"
	if _, err := os.Stat(claudeBin); err != nil {
		t.Skip("claude not available:", err)
	}

	ptyExec := serve.NewPTYExecutor(claudeBin,
		serve.WithResponseTimeout(2*time.Minute),
		serve.WithStableDelay(3*time.Second),
	)
	defer ptyExec.Close()

	srv := serve.NewServer(ptyExec, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	addr, _ := srv.Start(ctx)
	if addr == "" {
		t.Fatal("server failed to start")
	}
	t.Logf("server listening on %s", addr)

	client := openai.NewClient(
		option.WithBaseURL("http://"+addr+"/v1"),
		option.WithAPIKey("test-key"),
	)

	resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Model: shared.ResponsesModel("anthropic/claude"),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("respond with exactly the word PONG and nothing else"),
		},
	})
	if err != nil {
		t.Fatalf("Responses.New failed: %v", err)
	}

	t.Logf("response status: %s", resp.Status)
	t.Logf("output text (%d bytes): %s", len(resp.OutputText()), resp.OutputText())

	if resp.Status != "completed" {
		t.Errorf("status = %q, want completed", resp.Status)
	}

	output := resp.OutputText()
	if !strings.Contains(strings.ToUpper(output), "PONG") {
		t.Errorf("expected PONG in response; got:\n%s", output)
	}
}
