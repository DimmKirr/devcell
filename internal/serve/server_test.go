package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/version"
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
	var body HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	// Version fields must be populated even without ldflags injection
	// (defaults: v0.0.0, none, unknown). They're informational, never empty.
	if body.VersionTag == "" {
		t.Error("version_tag should never be empty (default v0.0.0)")
	}
	if body.Commit == "" {
		t.Error("commit should never be empty (default 'none')")
	}
	if body.BuildDate == "" {
		t.Error("build_date should never be empty (default 'unknown')")
	}
	if body.Version == "" {
		t.Error("version composite should never be empty")
	}
}

// TestHealth_VersionMatchesLdflagsInjection proves the response reflects
// values from internal/version (which the build pipeline overrides via
// -ldflags). We mutate the package vars directly here to simulate that
// injection without rebuilding.
// TestServer_LogPromptsToggle verifies the server stores the LogPrompts
// flag and threads it into the handlers. We don't assert on log output
// directly (the logger captures stderr at init time and isn't trivially
// redirectable mid-test) — instead we verify the wiring contract.
func TestServer_LogPromptsToggle(t *testing.T) {
	srv := NewServer(&fakeExec{}, 0)
	if srv.logPrompts {
		t.Error("logPrompts should default to false")
	}
	srv.SetLogPrompts(true)
	if !srv.logPrompts {
		t.Error("SetLogPrompts(true) did not flip the field")
	}
	srv.SetLogPrompts(false)
	if srv.logPrompts {
		t.Error("SetLogPrompts(false) did not flip the field")
	}
}

func TestHealth_VersionMatchesLdflagsInjection(t *testing.T) {
	// Patch package-level version vars (same vars that `-X .../version.GitCommit=...`
	// writes at link time) and restore on cleanup.
	origV, origC, origD := version.Version, version.GitCommit, version.BuildDate
	t.Cleanup(func() {
		version.Version = origV
		version.GitCommit = origC
		version.BuildDate = origD
	})
	version.Version = "v9.9.9"
	version.GitCommit = "deadbeef"
	version.BuildDate = "2026-04-26"

	fe := &fakeExec{}
	srv := NewServer(fe, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, _ := srv.Start(ctx)

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	var body HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.VersionTag != "v9.9.9" {
		t.Errorf("version_tag = %q, want v9.9.9", body.VersionTag)
	}
	if body.Commit != "deadbeef" {
		t.Errorf("commit = %q, want deadbeef", body.Commit)
	}
	if body.BuildDate != "2026-04-26" {
		t.Errorf("build_date = %q, want 2026-04-26", body.BuildDate)
	}
	want := "v9.9.9-2026-04-26-deadbeef"
	if body.Version != want {
		t.Errorf("version = %q, want %q", body.Version, want)
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
