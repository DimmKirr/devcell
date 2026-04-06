package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestServer_ListensOnConfiguredPort(t *testing.T) {
	fe := &fakeExec{}
	srv := NewServer(fe, 0) // 0 = let OS pick a free port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, errCh := srv.Start(ctx)
	if addr == "" {
		t.Fatal("expected non-empty address")
	}

	resp, err := http.Get("http://" + addr + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("server error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("server did not shut down in time")
	}
}

func TestServer_DefaultPort(t *testing.T) {
	if DefaultPort != 8484 {
		t.Errorf("DefaultPort = %d, want 8484", DefaultPort)
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	fe := &fakeExec{}
	srv := NewServer(fe, 0)

	ctx, cancel := context.WithCancel(context.Background())
	addr, errCh := srv.Start(ctx)

	// Verify it's running.
	resp, err := http.Get("http://" + addr + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()

	// Cancel context — server should shut down cleanly.
	cancel()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("server error on shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("server did not shut down in time")
	}
}

func TestHealth_Returns200(t *testing.T) {
	fe := &fakeExec{}
	srv := NewServer(fe, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := srv.Start(ctx)

	resp, err := http.Get("http://" + addr + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("status = %q, want %q", body["status"], "ok")
	}
}

func TestModels_Returns200(t *testing.T) {
	fe := &fakeExec{}
	srv := NewServer(fe, 0)
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

	resp, err := http.Get("http://" + addr + "/v1/models")
	if err != nil {
		t.Fatalf("GET /models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Object != "list" {
		t.Errorf("object = %q, want %q", body.Object, "list")
	}
	if len(body.Data) == 0 {
		t.Error("expected models in data")
	}
	var foundSonnet bool
	for _, m := range body.Data {
		if m.ID == "anthropic/sonnet" {
			foundSonnet = true
		}
	}
	if !foundSonnet {
		t.Error("expected anthropic/sonnet in models list")
	}
}

func TestHealth_MethodNotAllowed(t *testing.T) {
	fe := &fakeExec{}
	srv := NewServer(fe, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := srv.Start(ctx)

	resp, err := http.Post("http://"+addr+"/api/v1/health", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}
