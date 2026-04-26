package serve_test

import (
	"context"
	"testing"

	"github.com/DimmKirr/devcell/internal/serve"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// fakeExecResponses captures input for assertions and returns canned output.
type fakeExecResponses struct {
	gotAgent  string
	gotPrompt string
	gotModel  string
	gotEffort string
	stdout    string
}

func (f *fakeExecResponses) Run(opts serve.ExecOpts) serve.ExecResult {
	f.gotAgent = opts.Agent
	f.gotPrompt = opts.Prompt
	f.gotModel = opts.Model
	f.gotEffort = opts.Effort
	return serve.ExecResult{Stdout: f.stdout}
}

// TestOpenAISDK_Responses_StringInput verifies the official openai-go SDK
// can call client.Responses.New against our /v1/responses endpoint.
func TestOpenAISDK_Responses_StringInput(t *testing.T) {
	fe := &fakeExecResponses{stdout: "Hello, world!"}
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

	resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Model: shared.ResponsesModel("anthropic/sonnet"),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("Say hello."),
		},
	})
	if err != nil {
		t.Fatalf("Responses.New failed: %v", err)
	}

	if resp.Object != "response" {
		t.Errorf("object = %q, want response", resp.Object)
	}
	if resp.Status != "completed" {
		t.Errorf("status = %q, want completed", resp.Status)
	}
	if resp.OutputText() != "Hello, world!" {
		t.Errorf("OutputText() = %q, want %q", resp.OutputText(), "Hello, world!")
	}
	if len(resp.Output) != 1 {
		t.Fatalf("output len = %d, want 1", len(resp.Output))
	}
	if resp.Output[0].Type != "message" {
		t.Errorf("output[0].type = %q, want message", resp.Output[0].Type)
	}
}

// TestOpenAISDK_Responses_ModelRouting verifies SDK -> agent routing.
func TestOpenAISDK_Responses_ModelRouting(t *testing.T) {
	fe := &fakeExecResponses{stdout: "ok"}
	srv := serve.NewServer(fe, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := srv.Start(ctx)
	client := openai.NewClient(
		option.WithBaseURL("http://"+addr+"/v1"),
		option.WithAPIKey("test-key"),
	)

	_, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Model: shared.ResponsesModel("anthropic/sonnet"),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("hi"),
		},
	})
	if err != nil {
		t.Fatalf("Responses.New failed: %v", err)
	}
	if fe.gotAgent != "claude" {
		t.Errorf("agent = %q, want claude", fe.gotAgent)
	}
	if fe.gotModel != "sonnet" {
		t.Errorf("submodel = %q, want sonnet", fe.gotModel)
	}
}

// TestOpenAISDK_Responses_PreviousResponseIDIgnored verifies that sending
// previous_response_id (which devcell can't honor) doesn't error — the
// stateless server just ignores it.
func TestOpenAISDK_Responses_PreviousResponseIDIgnored(t *testing.T) {
	fe := &fakeExecResponses{stdout: "fresh response"}
	srv := serve.NewServer(fe, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := srv.Start(ctx)
	client := openai.NewClient(
		option.WithBaseURL("http://"+addr+"/v1"),
		option.WithAPIKey("test-key"),
	)

	resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Model: shared.ResponsesModel("anthropic/sonnet"),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("continue"),
		},
		PreviousResponseID: openai.String("resp_does_not_exist"),
		Temperature:        openai.Float(0.7),
		MaxOutputTokens:    openai.Int(100),
	})
	if err != nil {
		t.Fatalf("Responses.New failed: %v", err)
	}
	if resp.OutputText() != "fresh response" {
		t.Errorf("OutputText() = %q, want fresh response", resp.OutputText())
	}
}

// TestOpenAISDK_Responses_Instructions verifies system prompt routing.
func TestOpenAISDK_Responses_Instructions(t *testing.T) {
	fe := &fakeExecResponses{stdout: "brief"}
	srv := serve.NewServer(fe, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := srv.Start(ctx)
	client := openai.NewClient(
		option.WithBaseURL("http://"+addr+"/v1"),
		option.WithAPIKey("test-key"),
	)

	_, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Model:        shared.ResponsesModel("anthropic/sonnet"),
		Instructions: openai.String("be brief"),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("hello"),
		},
	})
	if err != nil {
		t.Fatalf("Responses.New failed: %v", err)
	}
	wantPrefix := "[system]: be brief\n[user]: hello\n"
	if fe.gotPrompt != wantPrefix {
		t.Errorf("prompt = %q, want %q", fe.gotPrompt, wantPrefix)
	}
}
