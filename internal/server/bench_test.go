package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"
)

func newBenchServer(b *testing.B) *Server {
	b.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 2})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		b.Skipf("skipping: redis not available: %v", err)
	}
	b.Cleanup(func() { rdb.FlushDB(context.Background()); rdb.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := prometheus.NewRegistry()
	s := &Server{
		redis:        rdb,
		logger:       logger,
		metrics:      NewMetrics(reg, rdb),
		metricsReg:   reg,
		consumerName: "bench-worker",
	}
	s.ensureConsumerGroup(context.Background())
	return s
}

// --- Prometheus Metrics Tests ---

func TestMetricsEndpoint(t *testing.T) {
	s := newTestServer(t)
	router := s.RegisterRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// GaugeFunc metrics are always exported; counters/histograms appear after first observation
	for _, metric := range []string{
		"pulseq_queue_depth",
		"pulseq_dlq_depth",
	} {
		if !strings.Contains(bodyStr, metric) {
			t.Errorf("expected /metrics to contain %q", metric)
		}
	}

	// trigger a counter so it appears, then verify
	s.metrics.JobsEnqueued.WithLabelValues("test").Inc()
	resp2, _ := http.Get(ts.URL + "/metrics")
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "pulseq_jobs_enqueued_total") {
		t.Error("expected pulseq_jobs_enqueued_total after increment")
	}
}

func TestEnqueueIncrementsMetric(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	s.ensureConsumerGroup(ctx)

	_, err := s.enqueueEmailJob(ctx, EmailJob{IP: "10.0.0.99", Reason: "test_metric"}, "metric-idem-1")
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	// read the counter value via the test registry
	val := getCounterValue(s.metrics.JobsEnqueued, "test_metric")
	if val != 1 {
		t.Errorf("expected JobsEnqueued=1, got %v", val)
	}
}

func getCounterValue(cv *prometheus.CounterVec, label string) float64 {
	m := &dto.Metric{}
	_ = cv.WithLabelValues(label).(prometheus.Metric).Write(m)
	if m.Counter != nil {
		return *m.Counter.Value
	}
	return 0
}

// --- Correlation ID Tests ---

func TestCorrelationIDMiddleware(t *testing.T) {
	s := newTestServer(t)
	router := s.RegisterRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	// request without correlation ID - should get one generated
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	cid := resp.Header.Get("X-Correlation-ID")
	if cid == "" {
		t.Error("expected X-Correlation-ID header to be set")
	}

	// request with a correlation ID - should be echoed back
	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Header.Set("X-Correlation-ID", "test-correlation-123")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.Header.Get("X-Correlation-ID") != "test-correlation-123" {
		t.Errorf("expected echoed correlation ID, got %q", resp2.Header.Get("X-Correlation-ID"))
	}
}

// --- Queue Latency Parsing ---

func TestParseStreamTimestamp(t *testing.T) {
	// Redis stream IDs are "<milliseconds>-<seq>"
	now := time.Now()
	id := fmt.Sprintf("%d-0", now.UnixMilli())
	ts, ok := parseStreamTimestamp(id)
	if !ok {
		t.Fatal("expected successful parse")
	}
	diff := now.Sub(ts)
	if diff < 0 || diff > time.Millisecond {
		t.Errorf("expected timestamp within 1ms, got diff=%v", diff)
	}

	// invalid ID
	_, ok = parseStreamTimestamp("notanumber-0")
	if ok {
		t.Error("expected parse to fail for invalid ID")
	}
}

// --- Dynamic Consumer Name ---

func TestDynamicConsumerName(t *testing.T) {
	s := newTestServer(t)
	if s.consumerName != "test-worker" {
		t.Errorf("expected consumer name 'test-worker', got %q", s.consumerName)
	}
}

// --- Stress Test ---

func TestStressEnqueue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	s := newTestServer(t)
	ctx := context.Background()
	s.ensureConsumerGroup(ctx)

	router := s.RegisterRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	const numRequests = 500
	var wg sync.WaitGroup
	var successes atomic.Int64
	var conflicts atomic.Int64

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := fmt.Sprintf(`{"ip":"stress-%d","reason":"stress_test"}`, i)
			resp, err := http.Post(ts.URL+"/jobs/enqueue", "application/json", strings.NewReader(payload))
			if err != nil {
				return
			}
			defer resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusAccepted:
				successes.Add(1)
			case http.StatusConflict:
				conflicts.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// each unique IP should succeed (500 unique IPs = 500 successes)
	if successes.Load() != numRequests {
		t.Errorf("expected %d successes, got %d (conflicts: %d)", numRequests, successes.Load(), conflicts.Load())
	}

	// verify queue depth
	qLen, _ := s.redis.XLen(ctx, emailQueue).Result()
	if qLen < int64(numRequests) {
		t.Errorf("expected at least %d messages in queue, got %d", numRequests, qLen)
	}
}

// --- Multiple Workers with Consumer Groups ---

func TestMultipleWorkerConsumers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-worker test in short mode")
	}

	s := newTestServer(t)
	ctx := context.Background()
	s.ensureConsumerGroup(ctx)

	// enqueue some jobs
	for i := 0; i < 20; i++ {
		s.redis.XAdd(ctx, &redis.XAddArgs{
			Stream: emailQueue,
			Values: map[string]any{
				"ip":              fmt.Sprintf("mw-%d", i),
				"reason":          "multi_worker_test",
				"attempts":        0,
				"idempotency_key": fmt.Sprintf("mw-key-%d", i),
			},
		})
	}

	// verify consumer group info
	groups, err := s.redis.XInfoGroups(ctx, emailQueue).Result()
	if err != nil {
		t.Fatalf("XInfoGroups failed: %v", err)
	}
	found := false
	for _, g := range groups {
		if g.Name == consumerGroup {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected consumer group to exist")
	}

	// verify pending entries via XINFO
	pending, _ := s.redis.XPending(ctx, emailQueue, consumerGroup).Result()
	t.Logf("pending count: %d", pending.Count)
}

// --- Benchmarks ---

func BenchmarkEnqueueJob(b *testing.B) {
	s := newBenchServer(b)
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ip := fmt.Sprintf("bench-%d-%d", time.Now().UnixNano(), i)
			s.enqueueEmailJob(ctx, EmailJob{IP: ip, Reason: "bench"}, fmt.Sprintf("idem-%s", ip))
			i++
		}
	})
}

func BenchmarkCheckDuplicate(b *testing.B) {
	s := newBenchServer(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job := EmailJob{IP: fmt.Sprintf("dedup-bench-%d", i), Reason: "bench"}
		s.CheckDuplicate(ctx, job)
	}
}

func BenchmarkHTTPEnqueue(b *testing.B) {
	s := newBenchServer(b)
	router := s.RegisterRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			payload := fmt.Sprintf(`{"ip":"http-bench-%d-%d","reason":"bench"}`, time.Now().UnixNano(), i)
			resp, err := http.Post(ts.URL+"/jobs/enqueue", "application/json", strings.NewReader(payload))
			if err == nil {
				resp.Body.Close()
			}
			i++
		}
	})
}
