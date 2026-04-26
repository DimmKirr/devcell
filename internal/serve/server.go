package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"time"

	"github.com/DimmKirr/devcell/internal/version"
	"github.com/swaggo/swag"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// DefaultPort is the default listen port for devcell serve.
const DefaultPort = 8484

// Server is the devcell HTTP API server.
type Server struct {
	exec       Executor
	port       int
	lookPath   LookPathFunc
	anthropic  AnthropicClient
	apiKey     string // empty = no auth
	logPrompts bool   // when true, handlers log full prompt + response bodies
}

// NewServer creates a Server. Use port=0 to let the OS pick a free port.
// Uses exec.LookPath for model discovery and RealAnthropicClient by default.
func NewServer(exec Executor, port int) *Server {
	return &Server{
		exec:      exec,
		port:      port,
		lookPath:  execLookPath,
		anthropic: &RealAnthropicClient{},
	}
}

// SetLookPath overrides the binary discovery function (for testing).
func (s *Server) SetLookPath(fn LookPathFunc) {
	s.lookPath = fn
}

// SetAnthropicClient overrides the Anthropic API client (for testing).
func (s *Server) SetAnthropicClient(ac AnthropicClient) {
	s.anthropic = ac
}

// SetAPIKey sets the API key for bearer auth. Empty disables auth.
func (s *Server) SetAPIKey(key string) {
	s.apiKey = key
}

// APIKey returns the configured API key.
func (s *Server) APIKey() string {
	return s.apiKey
}

// SetLogPrompts enables or disables full prompt + response body logging.
//
// When true, /v1/chat/completions and /v1/responses handlers log the
// assembled prompt and the model's reply at INFO level. Off by default —
// prompts often contain secrets, PII, or large pasted content from
// upstream tools (n8n flows, agents, etc.), so this is opt-in.
func (s *Server) SetLogPrompts(v bool) {
	s.logPrompts = v
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// Start begins listening and returns the address and an error channel.
// The server shuts down when ctx is cancelled.
func (s *Server) Start(ctx context.Context) (addr string, errCh chan error) {
	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", AuthMiddleware(s.apiKey, NewChatHandler(s.exec, s.logPrompts)))
	mux.Handle("/v1/responses", AuthMiddleware(s.apiKey, NewResponsesHandler(s.exec, s.logPrompts)))
	mux.Handle("/v1/models", AuthMiddleware(s.apiKey, NewModelsHandler(s.lookPath, s.anthropic)))
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/api/v1/health", healthHandler)
	mux.HandleFunc("/api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		doc, _ := swag.ReadDoc()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(doc))
	})
	mux.Handle("/swagger/", httpSwagger.Handler(
		httpSwagger.URL("/api/openapi.json"),
	))

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		errCh = make(chan error, 1)
		errCh <- err
		return "", errCh
	}

	srv := &http.Server{Handler: LoggingMiddleware(mux)}
	errCh = make(chan error, 1)

	go func() {
		errCh <- srv.Serve(ln)
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	return ln.Addr().String(), errCh
}

// HealthResponse is the health check response body.
//
// Version fields are injected at build time via -ldflags by the
// `task cell:build` / `task swag:generate` flow. An unbuilt-via-task binary
// will report defaults: version=v0.0.0, commit=none, build_date=unknown.
type HealthResponse struct {
	// Server status — "ok" when the server is running and ready to accept requests.
	Status string `json:"status" example:"ok"`
	// Composite version string matching `cell --version` output.
	// Format: `<version>-<build_date>-<commit>`.
	Version string `json:"version" example:"v0.1.0-2026-04-26-abc1234"`
	// Semantic version tag from the build (e.g. "v0.1.0").
	VersionTag string `json:"version_tag" example:"v0.1.0"`
	// Git commit hash this binary was built from.
	Commit string `json:"commit" example:"abc1234"`
	// Build date (UTC).
	BuildDate string `json:"build_date" example:"2026-04-26"`
}

// healthHandler handles GET /healthz and GET /api/v1/health.
//
// @Summary Health check
// @Description Returns server health status and the build version of the devcell binary
// @Description currently serving the request. No authentication required.
// @Description
// @Description The `version` field matches the output of `cell --version` exactly
// @Description (`<tag>-<build_date>-<commit>`). The individual `version_tag`, `commit`,
// @Description and `build_date` fields are also exposed for tooling that wants to parse them
// @Description without splitting the composite string. All values are injected at build time
// @Description via Go ldflags; a binary built outside the task pipeline reports defaults
// @Description (`v0.0.0`, `none`, `unknown`).
// @Description
// @Description Available at two paths:
// @Description - `/healthz` — Kubernetes convention for liveness/readiness probes and load balancers
// @Description - `/api/v1/health` — REST API convention for application-level client health checks
// @Tags health
// @Produce json
// @Success 200 {object} HealthResponse "Server is healthy"
// @Failure 405 {string} string "Only GET is allowed"
// @Router /healthz [get]
// @Router /api/v1/health [get]
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HealthResponse{
		Status:     "ok",
		Version:    version.Full(),
		VersionTag: version.Version,
		Commit:     version.GitCommit,
		BuildDate:  version.BuildDate,
	})
}
