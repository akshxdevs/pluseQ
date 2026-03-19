<!-- Improved compatibility of back to top link: See: https://github.com/othneildrew/Best-README-Template/pull/73 -->
<a id="readme-top"></a>
<!--
*** Thanks for checking out the Best-README-Template. If you have a suggestion
*** that would make this better, please fork the repo and create a pull request
*** or simply open an issue with the tag "enhancement".
*** Don't forget to give the project a star!
*** Thanks again! Now go create something AMAZING! :D
-->



<!-- PROJECT SHIELDS -->
<!--
*** I'm using markdown "reference style" links for readability.
*** Reference links are enclosed in brackets [ ] instead of parentheses ( ).
*** See the bottom of this document for the declaration of the reference variables
*** for contributors-url, forks-url, etc. This is an optional, concise syntax you may use.
*** https://www.markdownguide.org/basic-syntax/#reference-style-links
-->
[![Build](https://img.shields.io/badge/build-go%20test%20.%2F...-brightgreen)](https://github.com/akshxdevs/pulseQ)
[![Go Version](https://img.shields.io/badge/go-1.25.6-00ADD8?logo=go)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/akshxdevs/go-taskly)](https://goreportcard.com/report/github.com/akshxdevs/pluseQ)
[![Latest Release](https://img.shields.io/github/v/release/akshxdevs/go-taskly)](https://github.com/akshxdevs/pluseQ/releases)
[![License](https://img.shields.io/badge/license-MIT-yellow.svg)](LICENSE)



<!-- PROJECT LOGO -->
<br />
<div align="center">
  <a href="https://github.com/akshxdevs/pluse-q">
    <img src="./images/logo.svg" alt="pluse-q logo" width="120" height="120">
  </a>

  <h3 align="center">pluse-q</h3>

  <p align="center">
    A production-ready Go API server with Redis-backed job queuing, IP rate limiting, and background worker processing.
    <br />
    <a href="https://github.com/akshxdevs/pluse-q"><strong>Explore the docs »</strong></a>
    <br />
    <br />
    <a href="https://github.com/akshxdevs/pluse-q">View Demo</a>
    &middot;
    <a href="https://github.com/akshxdevs/pluse-q/issues/new?labels=bug&template=bug-report---.md">Report Bug</a>
    &middot;
    <a href="https://github.com/akshxdevs/pluse-q/issues/new?labels=enhancement&template=feature-request---.md">Request Feature</a>
  </p>
</div>



<!-- TABLE OF CONTENTS -->
<details>
  <summary>Table of Contents</summary>
  <ol>
    <li>
      <a href="#about-the-project">About The Project</a>
      <ul>
        <li><a href="#built-with">Built With</a></li>
      </ul>
    </li>
    <li>
      <a href="#getting-started">Getting Started</a>
      <ul>
        <li><a href="#prerequisites">Prerequisites</a></li>
        <li><a href="#installation">Installation</a></li>
      </ul>
    </li>
    <li><a href="#usage">Usage</a></li>
    <li>
      <a href="#architecture">Architecture</a>
      <ul>
        <li><a href="#system-design">System Design</a></li>
        <li><a href="#data-flow">Data Flow</a></li>
        <li><a href="#scaling-strategy">Scaling Strategy</a></li>
      </ul>
    </li>
    <li><a href="#tradeoffs">Tradeoffs</a></li>
    <li><a href="#roadmap">Roadmap</a></li>
    <li><a href="#contributing">Contributing</a></li>
    <li><a href="#license">License</a></li>
    <li><a href="#contact">Contact</a></li>
    <li><a href="#acknowledgments">Acknowledgments</a></li>
  </ol>
</details>



<!-- ABOUT THE PROJECT -->
## About The Project

**pluse-q** is a production-ready Go HTTP API server built around a Redis-backed asynchronous job queue. It tackles a real-world challenge: what happens when users hammer your login endpoint? Rather than simply rejecting excess requests and moving on, pluse-q detects abuse in real time, enqueues a background email notification job, and processes it with full reliability guarantees — retries, per-job timeouts, and a dead-letter queue — all without blocking the request lifecycle.

**The Problem it solves:**

Most API servers treat rate limiting as a dead end — too many requests, HTTP 429, done. There is no downstream action, no alerting, and no audit trail. pluse-q addresses this gap by treating every rate-limit violation as a signal worth acting on:

* When a client exceeds the allowed login attempts, an email notification job is enqueued into a Redis Stream — non-blocking and fire-and-forget from the handler's perspective.
* A dedicated background worker consumes jobs from the stream using a Redis consumer group, guaranteeing at-least-once delivery even across restarts.
* Failed jobs are retried up to 3 times, each with a 30-second processing timeout. Exhausted jobs land in a Dead Letter Queue (DLQ) for inspection and replay.
* Rate-limit counters are IP-scoped and auto-expire using Redis TTL, keeping state lightweight and self-cleaning with zero manual intervention.

<p align="right">(<a href="#readme-top">back to top</a>)</p>



### Built With

* [![Go][Go-badge]][Go-url]
* [![Redis][Redis-badge]][Redis-url]
* [![Chi][Chi-badge]][Chi-url]

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- GETTING STARTED -->
## Getting Started

### Prerequisites

* **Go 1.22+** — the module targets the latest Go toolchain
  ```sh
  go version
  ```
* **Redis** — used for rate limiting, session storage, and the job stream
  ```sh
  # Quickstart with Docker
  docker run -d -p 6379:6379 redis:alpine
  ```
* **(Optional) `air`** — live reload for development
  ```sh
  go install github.com/air-verse/air@latest
  ```

### Installation

1. Clone the repository
   ```sh
   git clone https://github.com/akshxdevs/pluse-q.git
   cd pluse-q
   ```
2. Create your environment file
   ```sh
   cp .env .env.local   # or edit .env directly
   ```
3. Set your environment variables in `.env`
   ```env
   PORT=8080
   APP_ENV=local
   REDIS_ADDR=localhost:6379
   REDIS_PASSWORD=
   ```
4. Build and run
   ```sh
   # Run directly
   make run

   # Or build a binary and execute it
   make build
   ./main
   ```
5. Run the test suite
   ```sh
   make test
   ```
6. Start with live reload during development
   ```sh
   make watch
   ```

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- USAGE EXAMPLES -->
## Usage

**Health check:**
```sh
curl http://localhost:8080/
# {"message":"Hello World"}
```

**Login (rate-limited endpoint):**
```sh
curl -X POST http://localhost:8080/api/v1/user/login \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"123random"}'
```

Successful response (`202 Accepted`):
```json
{
  "id": "dfe1e2e3-50c7-4a06-b122-e439294955b1",
  "username": "user482931",
  "email": "user@example.com",
  "created_at": "2026-03-09 12:00:00.000000000 +0000 UTC"
}
```

**Rate limit exceeded** — after 2 requests within the 10-second window from the same IP, all further attempts receive `429 Too Many Requests`:
```json
"Too many request. try again later"
```

On the 3rd excess request specifically, an email alert job is automatically and asynchronously enqueued into the Redis Stream — the caller never waits for it.

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- ARCHITECTURE -->
## Architecture

### System Design

pluse-q runs as a single binary containing both the HTTP server and the background worker. They share a Redis connection pool and a common lifecycle — the worker starts when the server boots and stops cleanly on shutdown. No external daemon, no sidecar, no second process to manage.

```
┌──────────────────────────────────────────────────────────────┐
│                           pluse-q                            │
│                                                              │
│  ┌───────────────────────────┐   ┌────────────────────────┐  │
│  │       HTTP Server         │   │   Background Worker    │  │
│  │    (Chi Router)           │   │   (goroutine)          │  │
│  │                           │   │                        │  │
│  │  GET  /                   │   │  XReadGroup            │  │
│  │  POST /api/v1/user/login  │   │  → processEmailJob()   │  │
│  │                           │   │  → XAck / retry / DLQ  │  │
│  │  Middleware stack:        │   │                        │  │
│  │  ├─ Logger                │   │                        │  │
│  │  ├─ CORS                  │   │                        │  │
│  │  └─ RateLimitMiddleware   │   │                        │  │
│  └────────────┬──────────────┘   └──────────┬─────────────┘  │
│               │                             │                 │
└───────────────┼─────────────────────────────┼─────────────────┘
                │                             │
                ▼                             ▼
       ┌─────────────────────────────────────────────┐
       │                   Redis                     │
       │                                             │
       │  rate_limit:{ip}   TTL counter (10s)        │
       │  user_auth:{sid}   Session store (1h TTL)   │
       │  email:queue       Redis Stream (job queue) │
       │  email:dlq         Dead Letter Queue        │
       └─────────────────────────────────────────────┘
```

**Project layout:**
```
pluse-q/
├── cmd/
│   └── api/
│       └── main.go          # Entrypoint: boot server + graceful shutdown
├── internal/
│   └── server/
│       ├── server.go        # Server struct, NewServer(), Redis + worker init
│       ├── routes.go        # Route registration, handlers, middleware
│       ├── worker.go        # Redis Streams consumer, retry logic, DLQ
│       ├── redis.go         # Redis client factory
│       └── routes_test.go   # Handler unit tests
├── .env                     # Environment configuration
├── .air.toml                # Live reload configuration
├── Makefile                 # build / run / test / watch targets
└── go.mod                   # Module definition and dependency graph
```

### Data Flow

**Login request with rate limiting:**

```
Client
  │
  │  POST /api/v1/user/login
  ▼
RateLimitMiddleware
  │
  ├─ GET  rate_limit:{ip}  (Redis)
  │
  ├─ INCR counter — set TTL=10s on first hit
  │
  ├─ cnt > 2 ? ──YES──► cnt == 3 ? ──YES──► XAdd email:queue  (goroutine, non-blocking)
  │              │                                │
  │              └────────────────────────────────►  HTTP 429 Too Many Requests
  │
  └─ cnt ≤ 2 ──► Login Handler
                    │
                    ├─ Validate email format
                    ├─ Check password
                    ├─ bcrypt hash
                    ├─ Generate session UUID
                    ├─ SET user_auth:{sessionId}  (Redis, TTL=1h)
                    └─ HTTP 202 Accepted  →  UserResponse JSON
```

**Background email worker lifecycle:**

```
StartWorker (goroutine — cancellable context tied to server shutdown)
  │
  ├─ XGroupCreateMkStream  →  ensure consumer group exists
  │
  └─ Loop:
       │
       ├─ XReadGroup (Group="email-workers", Consumer="worker-1", Block=2s, Count=1)
       │
       └─ handleMessage()
            │
            ├─ Parse job fields: ip, reason, attempts
            │
            ├─ processEmailJob() running in goroutine
            │       └─ select: done channel  |  30s jobTimeout
            │
            ├─ [success]                  →  XAck message
            │
            ├─ [error/timeout, attempts < 3]  →  XAck + re-enqueue (XAdd email:queue)
            │
            └─ [attempts ≥ 3]             →  XAck + sendToDLQ (XAdd email:dlq)
```

### Scaling Strategy

pluse-q is designed to scale incrementally without architectural rewrites:

| Concern | Current State | Scale-up Path |
|---|---|---|
| **Horizontal scaling** | Single instance | Run N replicas — Redis is the shared state layer, rate limiters and sessions work correctly across all instances |
| **Worker parallelism** | 1 goroutine, `worker-1` consumer | Register additional consumers (`worker-2`, `worker-3`) in the same group; Redis Streams distributes messages automatically |
| **Rate limit accuracy** | Redis `INCR` + `TTL` (atomic ops) | Inherently correct under horizontal scaling — Redis is the single source of truth, no per-instance state |
| **Session storage** | Redis key-value, 1h TTL | Upgrade to Redis Cluster or Sentinel for high availability; session semantics remain unchanged |
| **Job throughput** | `Count: 1` per read cycle | Increase `XReadGroupArgs.Count`; tune `Block` duration to trade latency for batch efficiency |
| **DLQ recovery** | Manual inspection | Build a DLQ replay consumer that re-enqueues `email:dlq` entries back into `email:queue` on demand |
| **Observability** | Structured log output | Add Prometheus metrics for queue depth, worker throughput, rate-limit hit rate, and DLQ size |

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- TRADEOFFS -->
## Tradeoffs

Every design decision in pluse-q involves deliberate tradeoffs. Here is the reasoning behind the key choices:

**Redis Streams vs. a dedicated message broker (Kafka, RabbitMQ)**
> Redis was chosen because it already serves as the rate-limit counter and session store — adding Kafka would introduce a second infrastructure dependency for a single job type. Redis Streams provide consumer groups, at-least-once delivery, and a DLQ pattern at a fraction of the operational complexity. The tradeoff: Redis Streams lack the durability guarantees, partition semantics, and long-term replay capabilities of Kafka. For high-throughput or event-sourcing use cases, Kafka is the right call.

**Embedded worker vs. a separate consumer service**
> Running the worker as a goroutine inside the API server simplifies deployment — one binary, one process, one container image. The tradeoff: the worker shares CPU and memory headroom with the HTTP server. Under heavy queue load, a saturated worker could impact request latency. Extracting it into a standalone binary is a low-friction refactor when that threshold is reached.

**Redis session store vs. JWT**
> Server-side sessions in Redis give instant revocation — delete the key, the session is gone. JWTs are stateless and avoid Redis reads on every request, but cannot be invalidated without a blocklist (which re-introduces Redis anyway). The current design favors control and security over raw performance — the right call for an auth-sensitive endpoint.

**IP-based rate limiting vs. user-based rate limiting**
> Rate limiting by `r.RemoteAddr` is simple and requires no authentication context upfront. The tradeoff: it is bypassable via IP rotation, and can wrongly penalise legitimate users behind shared NAT or a corporate proxy. User-based rate limiting (keyed by authenticated user ID) would be more precise but requires the auth check to run before the rate limit check — a chicken-and-egg problem for a login endpoint.

**Strict JSON decoding (`DisallowUnknownFields`)**
> Requests with unrecognised JSON fields are rejected outright. This prevents silent data corruption from mismatched client versions but makes the API less forgiving during active client development. The `/api/v1/` versioning prefix provides a stable contract per version, which mitigates this friction in practice.

**Graceful shutdown with a 5-second drain window**
> On `SIGINT`/`SIGTERM`, the server stops accepting new connections, allows in-flight HTTP requests up to 5 seconds to complete, cancels the worker context, and closes the Redis connection. The worker's 30-second per-job timeout means a job can theoretically outlive the drain window — any unacked message will be redelivered to the next consumer on restart (Redis Streams pending-entry list), so at-least-once delivery holds.

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- ROADMAP -->
## Roadmap

- [x] HTTP server with Chi router, Logger middleware, and CORS
- [x] Redis-backed IP rate limiting with auto-expiring TTL counters
- [x] Redis session storage on successful login
- [x] Redis Streams job queue for async email alerts on rate limit violations
- [x] Background worker with consumer group, 3-retry logic, per-job timeout, and DLQ
- [x] Graceful shutdown (SIGINT/SIGTERM) with 5-second drain window
- [ ] User-based rate limiting keyed by authenticated user ID
- [ ] PostgreSQL integration for persistent user storage
- [ ] JWT authentication alongside server-side session support
- [ ] DLQ replay consumer for automated failed-job recovery
- [ ] Prometheus metrics endpoint (queue depth, worker throughput, rate-limit hit rate)
- [ ] Docker Compose setup for local development (Redis + API in one command)
- [ ] OpenAPI / Swagger documentation

See the [open issues](https://github.com/akshxdevs/pluse-q/issues) for a full list of proposed features and known issues.

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- CONTRIBUTING -->
## Contributing

Contributions are what make the open source community such an amazing place to learn, inspire, and create. Any contributions you make are **greatly appreciated**.

If you have a suggestion that would make this better, please fork the repo and create a pull request. You can also simply open an issue with the tag "enhancement".
Don't forget to give the project a star! Thanks again!

1. Fork the Project
2. Create your Feature Branch (`git checkout -b feature/AmazingFeature`)
3. Commit your Changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the Branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

### Top contributors:

<a href="https://github.com/akshxdevs/pluse-q/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=akshxdevs/pluse-q" alt="contrib.rocks image" />
</a>

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- LICENSE -->
## License

Licensed under the [MIT License](LICENSE).

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- CONTACT -->
## Contact

Your Name - [@your_twitter](https://twitter.com/your_username) - email@example.com

Project Link: [https://github.com/akshxdevs/pluse-q](https://github.com/akshxdevs/pluse-q)

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- ACKNOWLEDGMENTS -->
## Acknowledgments

* [go-chi/chi](https://github.com/go-chi/chi) — Lightweight, idiomatic Go HTTP router with zero allocations on routing
* [redis/go-redis](https://github.com/redis/go-redis) — The Go Redis client powering the queue, rate limiter, and session store
* [google/uuid](https://github.com/google/uuid) — UUID generation for session identifiers
* [joho/godotenv](https://github.com/joho/godotenv) — `.env` file loading for local development
* [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) — bcrypt password hashing
* [air-verse/air](https://github.com/air-verse/air) — Live reload for Go applications during development
* [Img Shields](https://shields.io) — Badge generation for the README
* [othneildrew/Best-README-Template](https://github.com/othneildrew/Best-README-Template) — The README template this project is built on

<p align="right">(<a href="#readme-top">back to top</a>)</p>



<!-- MARKDOWN LINKS & IMAGES -->
<!-- https://www.markdownguide.org/basic-syntax/#reference-style-links -->
[contributors-shield]: https://img.shields.io/github/contributors/akshxdevs/pluse-q.svg?style=for-the-badge
[contributors-url]: https://github.com/akshxdevs/pluse-q/graphs/contributors
[forks-shield]: https://img.shields.io/github/forks/akshxdevs/pluse-q.svg?style=for-the-badge
[forks-url]: https://github.com/akshxdevs/pluse-q/network/members
[stars-shield]: https://img.shields.io/github/stars/akshxdevs/pluse-q.svg?style=for-the-badge
[stars-url]: https://github.com/akshxdevs/pluse-q/stargazers
[issues-shield]: https://img.shields.io/github/issues/akshxdevs/pluse-q.svg?style=for-the-badge
[issues-url]: https://github.com/akshxdevs/pluse-q/issues
[license-shield]: https://img.shields.io/badge/License-MIT-yellow.svg?style=for-the-badge
[license-url]: https://github.com/akshxdevs/pluse-q/blob/main/LICENSE
[linkedin-shield]: https://img.shields.io/badge/-LinkedIn-black.svg?style=for-the-badge&logo=linkedin&colorB=555
[linkedin-url]: https://linkedin.com/in/othneildrew
[Go-badge]: https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white
[Go-url]: https://go.dev/
[Redis-badge]: https://img.shields.io/badge/Redis-DC382D?style=for-the-badge&logo=redis&logoColor=white
[Redis-url]: https://redis.io/
[Chi-badge]: https://img.shields.io/badge/Chi_Router-007D9C?style=for-the-badge&logo=go&logoColor=white
[Chi-url]: https://go-chi.io/
