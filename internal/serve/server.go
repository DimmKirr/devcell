package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"time"
)

// DefaultPort is the default listen port for devcell serve.
const DefaultPort = 8484

// Server is the devcell HTTP API server.
type Server struct {
	exec      Executor
	port      int
	lookPath  LookPathFunc
	anthropic AnthropicClient
	apiKey    string // empty = no auth
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

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// Start begins listening and returns the address and an error channel.
// The server shuts down when ctx is cancelled.
func (s *Server) Start(ctx context.Context) (addr string, errCh chan error) {
	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", AuthMiddleware(s.apiKey, NewChatHandler(s.exec)))
	mux.Handle("/v1/models", AuthMiddleware(s.apiKey, NewModelsHandler(s.lookPath, s.anthropic)))
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		errCh = make(chan error, 1)
		errCh <- err
		return "", errCh
	}

	srv := &http.Server{Handler: mux}
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
