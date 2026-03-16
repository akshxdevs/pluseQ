package server

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	emailQueue    = "email:queue"
	emailDLQ      = "email:dlq"
	consumerGroup = "email-workers"
	consumerName  = "worker-1"
	maxRetries    = 3
)

type EmailJob struct {
	IP             string
	Reason         string
	Attempts       int
	IdempotencyKey string
}

func (s *Server) ensureConsumerGroup(ctx context.Context) {
	err := s.redis.XGroupCreateMkStream(ctx, emailQueue, consumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		log.Printf("failed to create consumer group: %v", err)
	}
}

func (s *Server) enqueueEmailJob(ctx context.Context, job EmailJob, idemKey string) error {
	ok, err := s.redis.SetNX(ctx, "email:idempotency:"+job.IP, idemKey, time.Second*10).Result()
	if err != nil {
		log.Println("redis error: idempotency check failed")
		return err
	}
	if !ok {
		log.Println("error: idempotency check failed")
		return nil
	}

	return s.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: emailQueue,
		Values: map[string]any{
			"ip":              job.IP,
			"reason":          job.Reason,
			"attempts":        job.Attempts,
			"idempotency_key": idemKey,
		},
	}).Err()
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
			"failed_at":       time.Now().UTC().String(),
		},
	}).Result()
	if err != nil {
		log.Printf("failed to send job to DLQ: %v", err)
		return
	}
	log.Printf("DLQ entry: %s | ip: %s | reason: %s | attempts: %d", dlq, job.IP, reason, job.Attempts)
	log.Printf("ALERT: job failed permanently for ip %s after %d attempts: %s", job.IP, job.Attempts, reason)
}

func (s *Server) processEmailJob(ctx context.Context, job EmailJob) error {
	log.Printf("sending email for ip %s (attempt %d)", job.IP, job.Attempts)
	log.Printf("ctx: %s", ctx)
	return fmt.Errorf("email provider unreachable")
}

func (s *Server) StartWorker(ctx context.Context) {
	s.ensureConsumerGroup(ctx)
	log.Println("email worker started")

	for {
		select {
		case <-ctx.Done():
			log.Println("email worker stopped")
			return
		default:
		}

		streams, err := s.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroup,
			Consumer: consumerName,
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
			log.Printf("worker read error: %v", err)
			continue
		}

		for _, stream := range streams {
			log.Println(len(stream.Stream))
			for _, msg := range stream.Messages {
				s.handleMessage(ctx, msg)
			}
		}
	}
}

func (s *Server) handleMessage(ctx context.Context, msg redis.XMessage) {
	log.Println("message: ", msg)

	ip, ok := msg.Values["ip"].(string)
	if !ok {
		log.Printf("malformed message: missing ip field, msg_id=%s", msg.ID)
		s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
		return
	}
	reason, _ := msg.Values["reason"].(string)
	attemptsStr, _ := msg.Values["attempts"].(string)
	attempts, _ := strconv.Atoi(attemptsStr)
	idempotencyKey, _ := msg.Values["idempotency_key"].(string)

	job := EmailJob{
		IP:             ip,
		Reason:         reason,
		Attempts:       attempts + 1,
		IdempotencyKey: idempotencyKey,
	}

	log.Printf("Attempts: %d", job.Attempts)
	log.Println("job: ", job)

	jobTimeout, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	done := make(chan error, 1)

	key := "email:sent:" + job.IP
	exist, _ := s.redis.Get(ctx, key).Result()
	if exist == "1" {
		s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
		log.Println("error: idempotency error")
		return
	}

	go func() {
		done <- s.processEmailJob(jobTimeout, job)
	}()

	var failReason string

	select {
	case err := <-done:
		if err == nil {
			log.Printf("job done for ip %s, acking", job.IP)
			s.redis.Set(ctx, key, "1", 30*time.Second)
			s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
			return
		}
		failReason = err.Error()
		log.Printf("job error for ip %s: %v", job.IP, err)
	case <-jobTimeout.Done():
		failReason = "timeout"
		log.Printf("job timed out for ip %s (attempt %d/%d)", job.IP, job.Attempts, maxRetries)
	}

	s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)

	if job.Attempts >= maxRetries {
		s.sendToDLQ(ctx, job, failReason)
		return
	}

	if err := s.enqueueEmailJob(ctx, job, idempotencyKey); err != nil {
		log.Printf("failed to re-enqueue job: %v", err)
	}
}
