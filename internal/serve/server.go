package serve

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
	apiKey       string // empty = no auth
	logPrompts   bool   // when true, handlers log full prompt + response bodies
	systemPrompt string // when non-empty, passed as --append-system-prompt to claude
	jobStore     *JobStore

	workspaceEnabled bool
	workspaceMock    bool
	workspaceHost    string

	tlsEnabled bool
	tlsCertDER []byte
	httpAddr   string
	debug      bool
}

// NewServer creates a Server. Use port=0 to let the OS pick a free port.
// Uses exec.LookPath for model discovery and RealAnthropicClient by default.
func NewServer(exec Executor, port int) *Server {
	return &Server{
		exec:      exec,
		port:      port,
		lookPath:  execLookPath,
		anthropic: &RealAnthropicClient{},
		jobStore:  NewJobStore(),
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

// SetSystemPrompt sets the operator-level system prompt passed to claude
// as --append-system-prompt on every /v1/chat/completions and /v1/responses
// request. Empty disables the flag (default). Composes with — does not
// override — any per-request `instructions` / `system` role from the body.
func (s *Server) SetSystemPrompt(p string) {
	s.systemPrompt = p
}

func (s *Server) SetWorkspace(enabled, mock bool, host string) {
	s.workspaceEnabled = enabled
	s.workspaceMock = mock
	s.workspaceHost = host
}

func (s *Server) SetTLS(v bool) {
	s.tlsEnabled = v
}

func (s *Server) SetDebug(v bool) {
	s.debug = v
}

func (s *Server) HTTPAddr() string {
	return s.httpAddr
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// Start begins listening and returns the address and an error channel.
// The server shuts down when ctx is cancelled.
func (s *Server) Start(ctx context.Context) (addr string, errCh chan error) {
	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", AuthMiddleware(s.apiKey, NewChatHandler(s.exec, s.logPrompts, s.systemPrompt)))
	mux.Handle("/v1/responses", AuthMiddleware(s.apiKey, NewResponsesHandler(s.exec, s.jobStore, s.logPrompts, s.systemPrompt)))
	mux.Handle("GET /v1/responses/{id}", AuthMiddleware(s.apiKey, NewResponseGetHandler(s.jobStore)))
	mux.Handle("POST /v1/responses/{id}/cancel", AuthMiddleware(s.apiKey, NewResponseCancelHandler(s.jobStore)))
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

	var tlsCert *tls.Certificate
	if s.tlsEnabled {
		var extraHosts []string
		if s.workspaceHost != "" {
			h := s.workspaceHost
			if idx := strings.LastIndex(h, ":"); idx != -1 {
				h = h[:idx]
			}
			extraHosts = append(extraHosts, h)
		}
		c, certErr := GenerateSelfSignedCert(extraHosts...)
		if certErr != nil {
			errCh = make(chan error, 1)
			errCh <- fmt.Errorf("generate TLS cert: %w", certErr)
			return "", errCh
		}
		tlsCert = &c
		s.tlsCertDER = c.Certificate[0]
	}

	if s.workspaceEnabled {
		var enum CellEnumerator
		if s.workspaceMock {
			enum = NewMockEnumerator()
		} else {
			var enumerators []CellEnumerator
			de, err := NewDockerEnumerator()
			if err != nil {
				log.Printf("workspace: docker unavailable: %v (remote registration still works)", err)
			} else {
				enumerators = append(enumerators, de)
			}
			enum = NewCompositeEnumerator(enumerators...)
		}
		{
			var wsOpts []WorkspaceOpt
			if s.tlsCertDER != nil {
				wsOpts = append(wsOpts, WithCertDER(s.tlsCertDER))
			}
			ws := WorkspaceRoutes(enum, s.workspaceHost, wsOpts...)
			mux.Handle("/{$}", ws)
			mux.Handle("/cert", ws)
			mux.Handle("/api/", ws)
			mux.Handle("/RDWeb/", ws)
			mux.Handle("/webfeed.aspx", ws)
			mux.Handle("/rdp/", ws)
			mux.Handle("/icons/", ws)
			mux.Handle("/preview/", ws)
		}
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		errCh = make(chan error, 1)
		errCh <- err
		return "", errCh
	}

	if tlsCert != nil {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{*tlsCert}})
	}

	var handler http.Handler = LoggingMiddleware(mux)
	if s.debug {
		handler = DebugLoggingMiddleware(mux)
	}
	srv := &http.Server{Handler: handler}
	errCh = make(chan error, 1)

	go func() {
		errCh <- srv.Serve(ln)
	}()

	// When TLS is enabled, start a plain HTTP listener on port-1 so
	// clients can download the self-signed cert without TLS bootstrapping.
	if s.tlsEnabled && s.tlsCertDER != nil {
		httpPort := s.port - 1
		if s.port == 0 {
			httpPort = 0
		}
		httpAddr := fmt.Sprintf(":%d", httpPort)
		httpLn, httpErr := net.Listen("tcp", httpAddr)
		if httpErr != nil {
			fmt.Fprintf(os.Stderr, "warning: HTTP listener on %s failed: %v\n", httpAddr, httpErr)
		} else {
			s.httpAddr = httpLn.Addr().String()
			httpSrv := &http.Server{Handler: handler}
			go func() {
				httpSrv.Serve(httpLn)
			}()
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				httpSrv.Shutdown(shutdownCtx)
			}()
		}
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	// Background-job TTL sweeper. Evicts completed/failed/cancelled jobs
	// older than 1h every minute, so the in-memory store doesn't grow
	// unbounded in long-lived serve processes. In-progress jobs are never
	// evicted regardless of age.
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.jobStore.Sweep(now, time.Hour)
			}
		}
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
