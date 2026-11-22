package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

type requestIDKey struct{}

func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", reqID)
		ctx := context.WithValue(r.Context(), requestIDKey{}, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func SlogMetricsMiddleware(logger *slog.Logger, metrics *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			dur := time.Since(start)

			route := "unmatched"
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				if pat := rctx.RoutePattern(); pat != "" {
					route = pat
				}
			}

			if metrics != nil {
				metrics.HTTPRequestsTotal.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
				metrics.HTTPRequestDuration.WithLabelValues(r.Method, route).Observe(dur.Seconds())
			}

			if logger != nil {
				logger.Info("http_request",
					slog.String("request_id", RequestIDFromContext(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("route", route),
					slog.Int("status", status),
					slog.Int("bytes", ww.BytesWritten()),
					slog.Float64("duration_ms", float64(dur.Microseconds())/1000.0),
				)
			}
		})
	}
}
