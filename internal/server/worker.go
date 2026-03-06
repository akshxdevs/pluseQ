package server

import (
	"context"
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
	IP       string
	Reason   string
	Attempts int
}

func (s *Server) ensureConsumerGroup(ctx context.Context) {
	err := s.redis.XGroupCreateMkStream(ctx, emailQueue, consumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		log.Printf("failed to create consumer group: %v", err)
	}
}

func (s *Server) enqueueEmailJob(ctx context.Context, job EmailJob) error {
	return s.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: emailQueue,
		Values: map[string]any{
			"ip":       job.IP,
			"reason":   job.Reason,
			"attempts": job.Attempts,
		},
	}).Err()
}

func (s *Server) sendToDLQ(ctx context.Context, job EmailJob, reason string) {
	err := s.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: emailDLQ,
		Values: map[string]any{
			"ip":        job.IP,
			"reason":    job.Reason,
			"attempts":  job.Attempts,
			"failure":   reason,
			"failed_at": time.Now().UTC().String(),
		},
	}).Err()
	if err != nil {
		log.Printf("failed to send job to DLQ: %v", err)
		return
	}
	log.Printf("job for ip %s sent to DLQ after %d attempts", job.IP, job.Attempts)
}

func (s *Server) processEmailJob(job EmailJob) error {

	log.Printf("sending email for ip %s (attempt %d)", job.IP, job.Attempts)
	return nil
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
			for _, msg := range stream.Messages {
				s.handleMessage(ctx, msg)
			}
		}
	}
}

func (s *Server) handleMessage(ctx context.Context, msg redis.XMessage) {
	attempts, _ := strconv.Atoi(msg.Values["attempts"].(string))
	job := EmailJob{
		IP:       msg.Values["ip"].(string),
		Reason:   msg.Values["reason"].(string),
		Attempts: attempts + 1,
	}

	err := s.processEmailJob(job)
	if err == nil {
		s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
		return
	}

	log.Printf("job failed for ip %s: %v (attempt %d/%d)", job.IP, err, job.Attempts, maxRetries)

	if job.Attempts >= maxRetries {
		s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
		s.sendToDLQ(ctx, job, err.Error())
		return
	}

	// re-enqueue with updated attempt count
	s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
	if err := s.enqueueEmailJob(ctx, job); err != nil {
		log.Printf("failed to re-enqueue job: %v", err)
	}
}
