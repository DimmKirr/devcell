package serve

import (
	"net/http"
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
