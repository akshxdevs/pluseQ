<a id="readme-top"></a>

[![Build](https://img.shields.io/badge/build-go%20test%20.%2F...-brightgreen)](https://github.com/akshxdevs/pluse-q)
[![Go Version](https://img.shields.io/badge/go-1.25.7-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-yellow.svg)](LICENSE)

<div align="center">
  <a href="https://github.com/akshxdevs/pluse-q">
    <img src="./images/logo.svg" alt="pluse-q logo" width="120" height="120">
  </a>

  <h1 align="center">pluse-q</h1>

  <p align="center">
    A Go HTTP API with Redis-backed rate limiting, async job processing, Prometheus metrics, and DLQ recovery.
    <br />
    <a href="https://github.com/akshxdevs/pluse-q/issues">Report Bug</a>
    &middot;
    <a href="https://github.com/akshxdevs/pluse-q/issues">Request Feature</a>
  </p>
</div>

## Overview

`pluse-q` is a Go service that treats rate-limit violations as events worth processing, not just requests worth rejecting.

Most login rate limiters stop at `429 Too Many Requests`. This project goes further: it stores rate-limit state in Redis, enqueues an asynchronous job when abuse crosses a threshold, processes that job with retries and idempotency controls, exposes queue metrics, and keeps job status available for inspection.

The result is a small but realistic backend that combines request handling, background processing, observability, and operational recovery in one service.

## Features

- Redis-backed IP rate limiting on `POST /api/v1/user/login` with a 10-second TTL window
- Async email-style job queue using Redis Streams and a consumer group
- Dead letter queue support with replay via `POST /jobs/dlq/replay`
- Job status tracking via `GET /jobs/{id}/status`
- Job deduplication and idempotency guards during enqueue and processing
- Exponential backoff retries up to 5 attempts before DLQ handoff
- Redis-backed session key storage on successful login
- Prometheus metrics at `GET /metrics`
- Correlation ID middleware with `X-Correlation-ID` propagation
- Structured JSON logging with request and worker lifecycle events
- Graceful shutdown for both the HTTP server and background worker

## Tech Stack

- Go 1.25.7
- Chi router and middleware
- Redis for rate limits, queue streams, DLQ, and job/session state
- Prometheus client library for metrics
- `bcrypt` and `uuid` utilities

## Project Structure

```text
pluse-q/
├── cmd/api/main.go
├── internal/server/
│   ├── server.go
│   ├── routes.go
│   ├── worker.go
│   ├── metrics.go
│   ├── logging.go
│   ├── redis.go
│   ├── routes_test.go
│   └── bench_test.go
├── images/logo.svg
├── Makefile
├── README.md
└── LICENSE
```

## Problem and Approach

The project is centered on one question: what should happen after a rate-limit breach?

Instead of treating the breach as the end of the flow, `pluse-q` turns it into a queued event:

- The login endpoint is rate-limited by client IP in Redis.
- Once the threshold is crossed, the API returns `429` immediately.
- On the next threshold breach, the server also enqueues a background job into a Redis Stream.
- A worker consumes jobs, tracks status, retries failures with backoff, and sends exhausted jobs to a DLQ.
- Metrics and structured logs make the queue behavior observable while the service is running.

## Getting Started

### Prerequisites

- Go 1.25.7 or newer
- Redis running locally on `localhost:6379`
- Optional: `air` for live reload during development

### Installation

```sh
git clone https://github.com/akshxdevs/pluse-q.git
cd pluse-q
```

### Configuration

The service reads configuration from environment variables:

| Variable | Required | Description |
|---|---|---|
| `PORT` | Yes | HTTP port for the API server |
| `REDIS_ADDR` | Yes | Redis address, for example `localhost:6379` |
| `REDIS_PASSWORD` | No | Redis password, if enabled |
| `WORKER_NAME` | No | Consumer name for the background worker; defaults to `worker-<hostname>-<pid>` |

Example:

```sh
export PORT=8080
export REDIS_ADDR=localhost:6379
export REDIS_PASSWORD=
export WORKER_NAME=worker-local
```

## Development Commands

```sh
make build   # build ./main from cmd/api/main.go
make run     # run the API server
make test    # run the Go test suite
make watch   # live reload with air (installs air interactively if missing)
make clean   # remove the built binary
```

## Usage

### Start the server

```sh
make run
```

### Health check

```sh
curl http://localhost:8080/
```

Response:

```json
{"message":"Hello World"}
```

### Login request

```sh
curl -X POST http://localhost:8080/api/v1/user/login \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"123random"}'
```

Successful response:

```json
{
  "id": "dfe1e2e3-50c7-4a06-b122-e439294955b1",
  "username": "user482931",
  "email": "user@example.com",
  "created_at": "2026-03-09 12:00:00.000000000 +0000 UTC"
}
```

When the per-IP threshold is exceeded within the 10-second window, the endpoint returns:

```json
"Too many request. try again later"
```

## API Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/` | Health-style hello world response |
| `GET` | `/metrics` | Prometheus metrics for HTTP and queue behavior |
| `POST` | `/api/v1/user/login` | Rate-limited login flow |
| `POST` | `/jobs/enqueue` | Manually enqueue a job |
| `POST` | `/jobs/dlq/replay` | Replay all jobs from the dead letter queue |
| `GET` | `/jobs/{id}/status` | Fetch stored status for a specific job |

Manual enqueue example:

```sh
curl -X POST http://localhost:8080/jobs/enqueue \
  -H "Content-Type: application/json" \
  -d '{"ip":"10.0.0.50","reason":"rate_limit"}'
```

DLQ replay example:

```sh
curl -X POST http://localhost:8080/jobs/dlq/replay
```

Job status example:

```sh
curl http://localhost:8080/jobs/<job-id>/status
```

## Architecture

`pluse-q` runs as a single process containing both the HTTP server and a background worker. Redis is the shared state layer for request throttling, stream-based jobs, DLQ storage, idempotency keys, and job status metadata.

```text
HTTP request
  -> Chi router
  -> correlation ID middleware
  -> request logging
  -> Prometheus middleware
  -> route handler
       -> Redis rate-limit state
       -> optional Redis Stream enqueue

Background worker
  -> Redis consumer group read
  -> status update: pending -> processing -> completed/failed
  -> retry with exponential backoff
  -> DLQ on retry exhaustion
```

### Queue Behavior

- Queue stream: `email:queue`
- DLQ stream: `email:dlq`
- Consumer group: `email-workers`
- Max retries: `5`
- Processing timeout per job: `30s`
- Max worker concurrency: `10`
- Job status TTL: `24h`
- Dedup TTL: `60s`

## Observability

The service ships with built-in metrics and request tracing support.

### Metrics

The `/metrics` endpoint exposes Prometheus metrics including:

- `pulseq_jobs_enqueued_total`
- `pulseq_jobs_processed_total`
- `pulseq_jobs_in_flight`
- `pulseq_queue_depth`
- `pulseq_dlq_depth`
- `pulseq_job_duration_seconds`
- `pulseq_queue_latency_seconds`
- `pulseq_http_request_duration_seconds`

### Logging and Correlation IDs

- Every request passes through correlation ID middleware.
- Incoming `X-Correlation-ID` headers are preserved; otherwise a UUID is generated.
- The response echoes `X-Correlation-ID`.
- Server and worker logs include correlation data where available.

## Testing

```sh
make test
```

The test suite covers:

- queue enqueue and dedup behavior
- DLQ replay
- job status tracking
- Prometheus metrics exposure
- correlation ID middleware
- queue latency parsing
- selected worker and HTTP flows

Redis-backed tests are skipped automatically when Redis is not available locally.

## Contributing

Open an issue or submit a pull request if you want to extend the queue flow, observability, or auth behavior.

## License

Licensed under the [MIT License](LICENSE).
