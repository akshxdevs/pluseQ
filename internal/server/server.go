package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"
	"github.com/redis/go-redis/v9"
)

type Server struct {
	port         int
	redis        *redis.Client
	workerCancel context.CancelFunc
}

func NewServer() (*http.Server, func()) {
	port, _ := strconv.Atoi(os.Getenv("PORT"))
	s := &Server{
		port:  port,
		redis: NewRedis(),
	}

	// start worker with its own cancellable context
	workerCtx, cancel := context.WithCancel(context.Background())
	s.workerCancel = cancel
	go s.StartWorker(workerCtx)

	// Declare Server config
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.RegisterRoutes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	cleanup := func() {
		s.workerCancel()
		if err := s.redis.Close(); err != nil {
			log.Printf("error closing redis: %v", err)
		}
	}

	return server, cleanup
}
