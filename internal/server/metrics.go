package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

const gaugeFuncTimeout = 500 * time.Millisecond

type Metrics struct {
	JobsEnqueued   *prometheus.CounterVec
	JobsProcessed  *prometheus.CounterVec
	JobsInFlight   prometheus.Gauge
	QueueDepth     prometheus.GaugeFunc
	DLQDepth       prometheus.GaugeFunc
	JobDuration    *prometheus.HistogramVec
	QueueLatency   *prometheus.HistogramVec
	HTTPRequestDur *prometheus.HistogramVec
}

func NewMetrics(reg prometheus.Registerer, rdb *redis.Client) *Metrics {
	m := &Metrics{
		JobsEnqueued: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pulseq_jobs_enqueued_total",
			Help: "Total number of jobs enqueued",
		}, []string{"reason"}),

		JobsProcessed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pulseq_jobs_processed_total",
			Help: "Total number of jobs processed",
		}, []string{"status"}),

		JobsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pulseq_jobs_in_flight",
			Help: "Number of jobs currently being processed",
		}),

		QueueDepth: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "pulseq_queue_depth",
			Help: "Number of messages in the main queue",
		}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), gaugeFuncTimeout)
			defer cancel()
			n, err := rdb.XLen(ctx, emailQueue).Result()
			if err != nil {
				return 0
			}
			return float64(n)
		}),

		DLQDepth: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "pulseq_dlq_depth",
			Help: "Number of messages in the dead letter queue",
		}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), gaugeFuncTimeout)
			defer cancel()
			n, err := rdb.XLen(ctx, emailDLQ).Result()
			if err != nil {
				return 0
			}
			return float64(n)
		}),

		JobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pulseq_job_duration_seconds",
			Help:    "Duration of job processing in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		}, []string{"status"}),

		QueueLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pulseq_queue_latency_seconds",
			Help:    "Time from enqueue to processing start in seconds",
			Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		}, []string{"reason"}),

		HTTPRequestDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pulseq_http_request_duration_seconds",
			Help:    "Duration of HTTP requests in seconds",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "path", "status_code"}),
	}

	reg.MustRegister(
		m.JobsEnqueued,
		m.JobsProcessed,
		m.JobsInFlight,
		m.QueueDepth,
		m.DLQDepth,
		m.JobDuration,
		m.QueueLatency,
		m.HTTPRequestDur,
	)

	return m
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func (s *Server) PrometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.metrics == nil {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		// use chi route pattern to avoid high-cardinality labels from path params
		routePattern := "unknown"
		if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePattern() != "" {
			routePattern = rctx.RoutePattern()
		}

		s.metrics.HTTPRequestDur.WithLabelValues(
			r.Method,
			routePattern,
			strconv.Itoa(rw.statusCode),
		).Observe(time.Since(start).Seconds())
	})
}
