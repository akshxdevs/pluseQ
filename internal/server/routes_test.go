package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestHandler(t *testing.T) {
	s := &Server{}
	server := httptest.NewServer(http.HandlerFunc(s.HelloWorldHandler))
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("error making request to server. Err: %v", err)
	}
	defer resp.Body.Close()
	// Assertions
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status OK; got %v", resp.Status)
	}
	expected := "{\"message\":\"Hello World\"}"
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("error reading response body. Err: %v", err)
	}
	if expected != string(body) {
		t.Errorf("expected response body to be %v; got %v", expected, string(body))
	}
}

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 1})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("skipping: redis not available: %v", err)
	}
	t.Cleanup(func() { rdb.FlushDB(context.Background()); rdb.Close() })
	return rdb
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{redis: newTestRedis(t)}
}

// --- Job Status Tracking ---

func TestSetJobStatus(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	s.setJobStatus(ctx, "test-123", StatusPending, "")

	status, err := s.redis.HGetAll(ctx, "job:status:test-123").Result()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status["status"] != StatusPending {
		t.Errorf("expected status %q, got %q", StatusPending, status["status"])
	}
	if status["updated_at"] == "" {
		t.Error("expected updated_at to be set")
	}

	s.setJobStatus(ctx, "test-123", StatusFailed, "connection refused")
	status, _ = s.redis.HGetAll(ctx, "job:status:test-123").Result()
	if status["status"] != StatusFailed {
		t.Errorf("expected status %q, got %q", StatusFailed, status["status"])
	}
	if status["error"] != "connection refused" {
		t.Errorf("expected error %q, got %q", "connection refused", status["error"])
	}
}

func TestEnqueueSetsStatusPending(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	s.ensureConsumerGroup(ctx)

	msgID, err := s.enqueueEmailJob(ctx, EmailJob{IP: "10.0.0.1", Reason: "test"}, "idem-1")
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	if msgID == "" {
		t.Fatal("expected non-empty message ID")
	}

	status, _ := s.redis.HGetAll(ctx, "job:status:"+msgID).Result()
	if status["status"] != StatusPending {
		t.Errorf("expected status %q, got %q", StatusPending, status["status"])
	}
}

func TestJobStatusHandler(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	s.setJobStatus(ctx, "job-abc", StatusCompleted, "")

	router := s.RegisterRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/jobs/job-abc/status")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// non-existent job
	resp2, err2 := http.Get(ts.URL + "/jobs/nonexistent/status")
	if err2 != nil {
		t.Fatalf("request failed: %v", err2)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp2.StatusCode)
	}
}

// --- Exponential Backoff ---

func TestBackoffDuration(t *testing.T) {
	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
	}
	for _, tc := range tests {
		got := backoffDuration(tc.attempt)
		if got != tc.expected {
			t.Errorf("backoffDuration(%d) = %v, want %v", tc.attempt, got, tc.expected)
		}
	}
}

// --- Job Deduplication ---

func TestCheckDuplicate(t *testing.T) {
	s := newTestServer(t)

	job := EmailJob{IP: "192.168.1.1", Reason: "rate_limit"}

	if s.CheckDuplicate(context.Background(), job) {
		t.Error("first call should not be a duplicate")
	}

	if !s.CheckDuplicate(context.Background(), job) {
		t.Error("second call should be a duplicate")
	}
}

func TestDedupDifferentPayloads(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	job1 := EmailJob{IP: "10.0.0.1", Reason: "rate_limit"}
	job2 := EmailJob{IP: "10.0.0.2", Reason: "rate_limit"}

	if s.CheckDuplicate(ctx, job1) {
		t.Error("job1 should not be duplicate")
	}
	if s.CheckDuplicate(ctx, job2) {
		t.Error("job2 should not be duplicate (different IP)")
	}
}

// --- DLQ Replay ---

func TestDLQReplay(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	s.ensureConsumerGroup(ctx)

	// put 2 entries into the DLQ with different IPs
	ips := []string{"10.0.0.1", "10.0.0.2"}
	for i, ip := range ips {
		s.redis.XAdd(ctx, &redis.XAddArgs{
			Stream: emailDLQ,
			Values: map[string]any{
				"ip":              ip,
				"reason":          "rate_limit",
				"attempts":        5,
				"failure":         "timeout",
				"idempotency_key": fmt.Sprintf("key-%d", i),
				"failed_at":       time.Now().UTC().Format(time.RFC3339),
			},
		})
	}

	count, err := s.ReplayDLQ(ctx)
	if err != nil {
		t.Fatalf("ReplayDLQ failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 replayed, got %d", count)
	}

	// DLQ should be empty now
	msgs, _ := s.redis.XRange(ctx, emailDLQ, "-", "+").Result()
	if len(msgs) != 0 {
		t.Errorf("expected DLQ to be empty, got %d messages", len(msgs))
	}

	// main queue should have entries
	qMsgs, _ := s.redis.XRange(ctx, emailQueue, "-", "+").Result()
	if len(qMsgs) < 2 {
		t.Errorf("expected at least 2 messages in queue, got %d", len(qMsgs))
	}
}

func TestEnqueueJobHandler(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	s.ensureConsumerGroup(ctx)

	router := s.RegisterRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	payload := `{"ip":"10.0.0.50","reason":"rate_limit"}`

	// first request should succeed
	resp, err := http.Post(ts.URL+"/jobs/enqueue", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, body)
	}

	// second request with same payload should return 409
	resp2, err := http.Post(ts.URL+"/jobs/enqueue", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp2.StatusCode)
	}
}

func TestEnqueueJobHandlerValidation(t *testing.T) {
	s := newTestServer(t)
	router := s.RegisterRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	// missing fields
	resp, err := http.Post(ts.URL+"/jobs/enqueue", "application/json", strings.NewReader(`{"ip":""}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDLQReplayHandler(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	s.ensureConsumerGroup(ctx)

	router := s.RegisterRoutes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/jobs/dlq/replay", "application/json", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"replayed":0}` {
		t.Errorf("unexpected body: %s", body)
	}
}
