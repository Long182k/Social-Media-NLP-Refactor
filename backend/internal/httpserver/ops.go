package httpserver

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

type PingFunc func(ctx context.Context) error

func (f PingFunc) Ping(ctx context.Context) error {
	return f(ctx)
}

type Metrics struct {
	Registry               *prometheus.Registry
	HTTPRequestsTotal      *prometheus.CounterVec
	HTTPRequestDuration    *prometheus.HistogramVec
	AuthLoginAttemptsTotal *prometheus.CounterVec
	NLPLocalDuration       prometheus.Histogram
	NLPDroppedRefinements  prometheus.Counter
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	httpReqs := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests processed, partitioned by method, route, and status.",
		},
		[]string{"method", "route", "status"},
	)
	httpDur := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency distributions.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)
	loginAttempts := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_login_attempts_total",
			Help: "Total login attempts partitioned by outcome.",
		},
		[]string{"outcome"},
	)
	nlpDur := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "nlp_local_duration_seconds",
			Help:    "Local lexicon sentiment scoring latency.",
			Buckets: []float64{0.000005, 0.00001, 0.000025, 0.00005, 0.0001, 0.0002, 0.0005, 0.001},
		},
	)

	nlpDropped := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "nlp_refinement_dropped_total",
		Help: "Sentiment refinement jobs dropped because the background queue is full.",
	})
	reg.MustRegister(httpReqs, httpDur, loginAttempts, nlpDur, nlpDropped)

	return &Metrics{
		Registry:               reg,
		HTTPRequestsTotal:      httpReqs,
		HTTPRequestDuration:    httpDur,
		AuthLoginAttemptsTotal: loginAttempts,
		NLPLocalDuration:       nlpDur,
		NLPDroppedRefinements:  nlpDropped,
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

func HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func ReadyzHandler(pg, valkey Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		var (
			wg        sync.WaitGroup
			pgErr     error
			valkeyErr error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			if pg == nil {
				pgErr = context.DeadlineExceeded
				return
			}
			pgErr = pg.Ping(ctx)
		}()
		go func() {
			defer wg.Done()
			if valkey == nil {
				valkeyErr = context.DeadlineExceeded
				return
			}
			valkeyErr = valkey.Ping(ctx)
		}()
		wg.Wait()

		pgState := "ok"
		if pgErr != nil {
			pgState = "error"
		}
		valkeyState := "ok"
		if valkeyErr != nil {
			valkeyState = "error"
		}

		if pgErr != nil || valkeyErr != nil {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status":   "not_ready",
				"postgres": pgState,
				"valkey":   valkeyState,
			})
			return
		}

		WriteJSON(w, http.StatusOK, map[string]string{
			"status":   "ready",
			"postgres": pgState,
			"valkey":   valkeyState,
		})
	}
}
