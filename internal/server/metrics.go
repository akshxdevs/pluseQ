package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

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
			n, err := rdb.XLen(context.Background(), emailQueue).Result()
			if err != nil {
				return 0
			}
			return float64(n)
		}),

		DLQDepth: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "pulseq_dlq_depth",
			Help: "Number of messages in the dead letter queue",
		}, func() float64 {
			n, err := rdb.XLen(context.Background(), emailDLQ).Result()
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

func (s *Server) PrometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.metrics == nil {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		s.metrics.HTTPRequestDur.WithLabelValues(
			r.Method,
			r.URL.Path,
			strconv.Itoa(rw.statusCode),
		).Observe(time.Since(start).Seconds())
	})
}
