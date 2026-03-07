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
	dlq, err := s.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: emailDLQ,
		Values: map[string]any{
			"ip":        job.IP,
			"reason":    job.Reason,
			"attempts":  job.Attempts,
			"failure":   reason,
			"failed_at": time.Now().UTC().String(),
		},
	}).Result()
	if err != nil {
		log.Printf("failed to send job to DLQ: %v", err)
		return
	}
	log.Printf("DLQ: %s", dlq)
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
			log.Println(len(stream.Stream))
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

	log.Printf("Attempts: %d", job.Attempts)
	log.Println("job: ", job)

	jobTimeout, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- s.processEmailJob(job)
	}()

	select {
	case err := <-done:
		if err == nil {
			log.Printf("job done for ip %s, acking", job.IP)
			s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
			return
		}
		log.Printf("job error for ip %s: %v", job.IP, err)
	case <-jobTimeout.Done():
		log.Printf("job timed out for ip %s (attempt %d/%d)", job.IP, job.Attempts, maxRetries)
	}

	if job.Attempts >= maxRetries {
		s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
		s.sendToDLQ(ctx, job, "timeout or error")
		return
	}

	s.redis.XAck(ctx, emailQueue, consumerGroup, msg.ID)
	if err := s.enqueueEmailJob(ctx, job); err != nil {
		log.Printf("failed to re-enqueue job: %v", err)
	}
}
