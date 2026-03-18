package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	emailQueue     = "email:queue"
	emailDLQ       = "email:dlq"
	consumerGroup  = "email-workers"
	maxRetries     = 5
	dedupTTL       = 60 * time.Second
	jobStatusTTL   = 24 * time.Hour
	maxConcurrency = 10
)

const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

type EmailJob struct {
	ID             string
	IP             string
	Reason         string
	Attempts       int
	IdempotencyKey string
}

func (s *Server) setJobStatus(ctx context.Context, jobID, status, errMsg string) {
	key := "job:status:" + jobID
	fields := map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	if errMsg != "" {
		fields["error"] = errMsg
	}
	if err := s.redis.HSet(ctx, key, fields).Err(); err != nil {
		s.logError(ctx, "job.status_update_failed", slog.String("job_id", jobID), slog.String("error", err.Error()))
		return
	}
	s.redis.Expire(ctx, key, jobStatusTTL)
}

func dedupKey(job EmailJob) string {
	data := fmt.Sprintf("%s:%s", job.IP, job.Reason)
	h := sha256.Sum256([]byte(data))
	return "job:dedup:" + hex.EncodeToString(h[:])
}

func (s *Server) CheckDuplicate(ctx context.Context, job EmailJob) bool {
	ok, err := s.redis.SetNX(ctx, dedupKey(job), "1", dedupTTL).Result()
	if err != nil {
		s.logError(ctx, "dedup.check_failed", slog.String("error", err.Error()))
		return false
	}
	return !ok
}

func (s *Server) ensureConsumerGroup(ctx context.Context) {
	err := s.redis.XGroupCreateMkStream(ctx, emailQueue, consumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		s.logError(ctx, "consumer_group.create_failed", slog.String("error", err.Error()))
	}
}

func (s *Server) enqueueEmailJob(ctx context.Context, job EmailJob, idemKey string) (string, error) {
	ok, err := s.redis.SetNX(ctx, "email:idempotency:"+job.IP, idemKey, time.Second*10).Result()
	if err != nil {
		s.logError(ctx, "enqueue.idempotency_failed", slog.String("ip", job.IP), slog.String("error", err.Error()))
		return "", err
	}
	if !ok {
		s.logInfo(ctx, "enqueue.idempotency_blocked", slog.String("ip", job.IP))
		return "", nil
	}

	msgID, err := s.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: emailQueue,
		Values: map[string]any{
			"ip":              job.IP,
			"reason":          job.Reason,
			"attempts":        job.Attempts,
			"idempotency_key": idemKey,
		},
	}).Result()
	if err != nil {
		return "", err
	}

	s.setJobStatus(ctx, msgID, StatusPending, "")

	if s.metrics != nil {
		s.metrics.JobsEnqueued.WithLabelValues(job.Reason).Inc()
	}

	s.logInfo(ctx, "job.enqueued",
		slog.String("job_id", msgID),
		slog.String("ip", job.IP),
		slog.String("reason", job.Reason),
	)

	return msgID, nil
}

func (s *Server) sendToDLQ(ctx context.Context, job EmailJob, reason string) {
	dlq, err := s.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: emailDLQ,
		Values: map[string]any{
			"ip":              job.IP,
			"reason":          job.Reason,
			"attempts":        job.Attempts,
			"failure":         reason,
			"idempotency_key": job.IdempotencyKey,
			"failed_at":       time.Now().UTC().Format(time.RFC3339),
		},
	}).Result()
	if err != nil {
		s.logError(ctx, "dlq.send_failed", slog.String("ip", job.IP), slog.String("error", err.Error()))
		return
	}
	s.setJobStatus(ctx, job.ID, StatusFailed, reason)

	if s.metrics != nil {
		s.metrics.JobsProcessed.WithLabelValues("dlq").Inc()
	}

	s.logError(ctx, "job.dlq",
		slog.String("dlq_id", dlq),
		slog.String("job_id", job.ID),
		slog.String("ip", job.IP),
		slog.String("failure", reason),
		slog.Int("attempts", job.Attempts),
	)
}

func (s *Server) ReplayDLQ(ctx context.Context) (int, error) {
	msgs, err := s.redis.XRange(ctx, emailDLQ, "-", "+").Result()
	if err != nil {
		return 0, fmt.Errorf("failed to read DLQ: %w", err)
	}

	replayed := 0
	for _, msg := range msgs {
		ip, _ := msg.Values["ip"].(string)
		reason, _ := msg.Values["reason"].(string)
		idemKey, _ := msg.Values["idempotency_key"].(string)

		job := EmailJob{IP: ip, Reason: reason, Attempts: 0}

		// clear dedup and idempotency keys before each enqueue
		// so multiple DLQ entries for the same IP can all be replayed
		s.redis.Del(ctx, dedupKey(job))
		s.redis.Del(ctx, "email:idempotency:"+ip)

		msgID, err := s.enqueueEmailJob(ctx, job, idemKey)
		if err != nil {
			s.logError(ctx, "dlq.replay_failed", slog.String("dlq_msg_id", msg.ID), slog.String("error", err.Error()))
			continue
		}
		if msgID == "" {
			s.logWarn(ctx, "dlq.replay_blocked", slog.String("dlq_msg_id", msg.ID))
			continue
		}

		s.redis.XDel(ctx, emailDLQ, msg.ID)
		replayed++
	}

	return replayed, nil
}

func (s *Server) processEmailJob(ctx context.Context, job EmailJob) error {
	s.logInfo(ctx, "job.executing",
		slog.String("job_id", job.ID),
		slog.String("ip", job.IP),
		slog.Int("attempt", job.Attempts),
	)
	return nil
}

// parseStreamTimestamp extracts the millisecond timestamp from a Redis stream ID (format: "1234567890123-0")
func parseStreamTimestamp(msgID string) (time.Time, bool) {
	parts := strings.SplitN(msgID, "-", 2)
	if len(parts) < 1 {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

func (s *Server) StartWorker(ctx context.Context) {
	s.ensureConsumerGroup(ctx)
	s.logInfo(ctx, "worker.started", slog.String("consumer", s.consumerName))

	sem := make(chan struct{}, maxConcurrency)

	for {
		select {
		case <-ctx.Done():
			s.logInfo(ctx, "worker.stopped", slog.String("consumer", s.consumerName))
			return
		default:
		}

		streams, err := s.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroup,
			Consumer: s.consumerName,
			Streams:  []string{emailQueue, ">"},
			Count:    1,
			Block:    2 * time.Second,
		}).Result()

		if err != nil {
			if err == redis.Nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			s.logError(ctx, "worker.read_error", slog.String("error", err.Error()))
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				sem <- struct{}{}
				go func(m redis.XMessage) {
					defer func() { <-sem }()
					s.handleMessage(ctx, m)
				}(msg)
			}
		}
	}
}

func backoffDuration(attempt int) time.Duration {
	return time.Second * (1 << uint(attempt-1))
}

func (s *Server) handleMessage(ctx context.Context, msg redis.XMessage) {
	ip, ok := msg.Values["ip"].(string)
	if !ok {
		s.logError(ctx, "job.malformed", slog.String("msg_id", msg.ID))
		s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
		return
	}
	reason, _ := msg.Values["reason"].(string)
	attemptsStr, _ := msg.Values["attempts"].(string)
	attempts, _ := strconv.Atoi(attemptsStr)
	idempotencyKey, _ := msg.Values["idempotency_key"].(string)

	job := EmailJob{
		ID:             msg.ID,
		IP:             ip,
		Reason:         reason,
		Attempts:       attempts + 1,
		IdempotencyKey: idempotencyKey,
	}

	// queue latency: time from enqueue (stream ID timestamp) to now
	if enqueueTime, ok := parseStreamTimestamp(msg.ID); ok && s.metrics != nil {
		latency := time.Since(enqueueTime)
		s.metrics.QueueLatency.WithLabelValues(reason).Observe(latency.Seconds())
	}

	if s.metrics != nil {
		s.metrics.JobsInFlight.Inc()
		defer s.metrics.JobsInFlight.Dec()
	}

	s.setJobStatus(ctx, msg.ID, StatusProcessing, "")
	s.logInfo(ctx, "job.processing",
		slog.String("job_id", msg.ID),
		slog.String("ip", job.IP),
		slog.Int("attempt", job.Attempts),
	)

	start := time.Now()

	jobTimeout, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	done := make(chan error, 1)

	key := "email:sent:" + job.IP
	exist, _ := s.redis.Get(ctx, key).Result()
	if exist == "1" {
		s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
		s.logWarn(ctx, "job.idempotency_skip", slog.String("job_id", msg.ID), slog.String("ip", job.IP))
		return
	}

	go func() {
		done <- s.processEmailJob(jobTimeout, job)
	}()

	var failReason string

	select {
	case err := <-done:
		if err == nil {
			duration := time.Since(start)
			s.redis.Set(ctx, key, "1", 30*time.Second)
			s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
			s.setJobStatus(ctx, msg.ID, StatusCompleted, "")

			if s.metrics != nil {
				s.metrics.JobsProcessed.WithLabelValues("completed").Inc()
				s.metrics.JobDuration.WithLabelValues("completed").Observe(duration.Seconds())
			}

			s.logInfo(ctx, "job.completed",
				slog.String("job_id", msg.ID),
				slog.String("ip", job.IP),
				slog.Float64("duration_ms", float64(duration.Milliseconds())),
			)
			return
		}
		failReason = err.Error()
		s.logError(ctx, "job.error",
			slog.String("job_id", msg.ID),
			slog.String("ip", job.IP),
			slog.String("error", failReason),
		)
	case <-jobTimeout.Done():
		failReason = "timeout"
		s.logError(ctx, "job.timeout",
			slog.String("job_id", msg.ID),
			slog.String("ip", job.IP),
			slog.Int("attempt", job.Attempts),
		)
	}

	duration := time.Since(start)
	if s.metrics != nil {
		s.metrics.JobsProcessed.WithLabelValues("failed").Inc()
		s.metrics.JobDuration.WithLabelValues("failed").Observe(duration.Seconds())
	}

	s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)

	if job.Attempts >= maxRetries {
		s.sendToDLQ(ctx, job, failReason)
		return
	}

	// exponential backoff: 1s, 2s, 4s, 8s, 16s
	backoff := backoffDuration(job.Attempts)
	s.logWarn(ctx, "job.retry",
		slog.String("job_id", msg.ID),
		slog.String("ip", job.IP),
		slog.Int("attempt", job.Attempts),
		slog.Float64("backoff_s", backoff.Seconds()),
	)
	select {
	case <-time.After(backoff):
	case <-ctx.Done():
		return
	}

	if _, err := s.enqueueEmailJob(ctx, job, idempotencyKey); err != nil {
		s.logError(ctx, "job.reenqueue_failed", slog.String("job_id", msg.ID), slog.String("error", err.Error()))
	}
}
