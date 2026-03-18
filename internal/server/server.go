package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

type Server struct {
	port         int
	redis        *redis.Client
	workerCancel context.CancelFunc
	metrics      *Metrics
	metricsReg   *prometheus.Registry
	logger       *slog.Logger
	consumerName string
}

func NewServer() (*http.Server, func()) {
	port, _ := strconv.Atoi(os.Getenv("PORT"))

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	rdb := NewRedis()

	consumerName := os.Getenv("WORKER_NAME")
	if consumerName == "" {
		hostname, _ := os.Hostname()
		consumerName = fmt.Sprintf("worker-%s-%d", hostname, os.Getpid())
	}

	reg := prometheus.NewRegistry()

	s := &Server{
		port:         port,
		redis:        rdb,
		metrics:      NewMetrics(reg, rdb),
		metricsReg:   reg,
		logger:       logger,
		consumerName: consumerName,
	}

	workerCtx, cancel := context.WithCancel(context.Background())
	s.workerCancel = cancel
	go s.StartWorker(workerCtx)

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
			logger.Error("error closing redis", slog.String("error", err.Error()))
		}
	}

	return server, cleanup
}
