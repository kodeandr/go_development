package api

import (
	"log/slog"
	"net/http"
	"time"
)

func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			defer func() {
				logger.Info("HTTP request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
