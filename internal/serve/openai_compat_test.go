package serve_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/DimmKirr/devcell/internal/serve"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// fakeExecCompat is a test executor for OpenAI compatibility tests.
type fakeExecCompat struct {
	stdout string
}

func (f *fakeExecCompat) Run(agent, prompt, model string) serve.ExecResult {
	return serve.ExecResult{Stdout: f.stdout}
}

// TestOpenAISDK_ChatCompletion verifies that the official OpenAI Go SDK
// can successfully communicate with our server — the real compatibility proof.
func TestOpenAISDK_ChatCompletion(t *testing.T) {
	fe := &fakeExecCompat{stdout: "The answer is 42."}
	srv := serve.NewServer(fe, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := srv.Start(ctx)
	if addr == "" {
		t.Fatal("server failed to start")
	}

	client := openai.NewClient(
		option.WithBaseURL("http://"+addr+"/v1"),
		option.WithAPIKey("test-key"),
	)

	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: "anthropic/sonnet",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("What is the meaning of life?"),
		},
	})
	if err != nil {
		t.Fatalf("OpenAI SDK request failed: %v", err)
	}

	if resp.Model != "anthropic/sonnet" {
		t.Errorf("model = %q, want %q", resp.Model, "anthropic/sonnet")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices len = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "The answer is 42." {
		t.Errorf("content = %q, want %q", resp.Choices[0].Message.Content, "The answer is 42.")
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("role = %q, want %q", resp.Choices[0].Message.Role, "assistant")
	}
}

// TestOpenAISDK_ModelRouting verifies agent/submodel routing works via SDK.
func TestOpenAISDK_ModelRouting(t *testing.T) {
	var gotAgent, gotModel string
	fe := &routingExec{onRun: func(agent, prompt, model string) {
		gotAgent = agent
		gotModel = model
	}}
	srv := serve.NewServer(fe, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := srv.Start(ctx)

	client := openai.NewClient(
		option.WithBaseURL("http://"+addr+"/v1"),
		option.WithAPIKey("test-key"),
	)

	_, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: "anthropic/sonnet",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("OpenAI SDK request failed: %v", err)
	}

	if gotAgent != "claude" {
		t.Errorf("agent = %q, want %q", gotAgent, "claude")
	}
	if gotModel != "sonnet" {
		t.Errorf("model = %q, want %q", gotModel, "sonnet")
	}
}

// TestOpenAISDK_ListModels verifies the SDK can list models from /v1/models.
func TestOpenAISDK_ListModels(t *testing.T) {
	fe := &fakeExecCompat{stdout: "ok"}
	srv := serve.NewServer(fe, 0)
	srv.SetLookPath(func(name string) (string, error) {
		if name == "claude" {
			return "/usr/bin/claude", nil
		}
		return "", fmt.Errorf("not found")
	})
	srv.SetAnthropicClient(nil) // use fallback models

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := srv.Start(ctx)

	client := openai.NewClient(
		option.WithBaseURL("http://"+addr+"/v1"),
		option.WithAPIKey("test-key"),
	)

	models, err := client.Models.List(ctx)
	if err != nil {
		t.Fatalf("OpenAI SDK list models failed: %v", err)
	}

	var foundSonnet bool
	for _, m := range models.Data {
		if m.ID == "anthropic/sonnet" {
			foundSonnet = true
		}
	}
	if !foundSonnet {
		t.Error("expected 'anthropic/sonnet' in models list via SDK")
	}
}

// routingExec captures agent/model routing for verification.
type routingExec struct {
	onRun func(agent, prompt, model string)
}

func (r *routingExec) Run(agent, prompt, model string) serve.ExecResult {
	if r.onRun != nil {
		r.onRun(agent, prompt, model)
	}
	return serve.ExecResult{Stdout: "ok"}
}
