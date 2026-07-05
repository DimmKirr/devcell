package serve

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/logger"
)

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// debugWriter captures the response body in addition to status code.
type debugWriter struct {
	http.ResponseWriter
	code int
	body bytes.Buffer
}

func (w *debugWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *debugWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// DebugLoggingMiddleware logs full request headers and response bodies to stderr.
func DebugLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var hdrs strings.Builder
		for k, vals := range r.Header {
			for _, v := range vals {
				fmt.Fprintf(&hdrs, "  %s: %s\n", k, v)
			}
		}
		var tlsInfo string
		if r.TLS != nil {
			tlsInfo = fmt.Sprintf("  TLS: version=0x%04x cipher=0x%04x SNI=%q\n", r.TLS.Version, r.TLS.CipherSuite, r.TLS.ServerName)
		}
		fmt.Fprintf(os.Stderr, "\n[DEBUG] >>> %s %s\n  Host: %s\n%s%s", r.Method, r.URL.String(), r.Host, tlsInfo, hdrs.String())

		dw := &debugWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(dw, r)

		duration := time.Since(start)
		respCT := dw.Header().Get("Content-Type")
		body := dw.body.String()
		if len(body) > 2000 {
			body = body[:2000] + "...(truncated)"
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] <<< %d (%s) Content-Type: %s\n%s\n", dw.code, duration, respCT, body)
	})
}

// LoggingMiddleware logs every HTTP request with method, path, status, and duration.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		duration := time.Since(start)

		if sw.code >= 500 {
			logger.Error("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.code,
				"duration", duration.String(),
			)
		} else if sw.code >= 400 {
			logger.Warn("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.code,
				"duration", duration.String(),
			)
		} else {
			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.code,
				"duration", duration.String(),
			)
		}
	})
}
