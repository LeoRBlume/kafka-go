# kafka-go

Two independent Go microservices communicating via Apache Kafka, built with ports & adapters architecture.

## Architecture

```
┌─────────────────┐        ┌───────────┐        ┌──────────────────┐
│    Producer     │──────▶ │   Kafka   │──────▶ │    Consumer      │
│   :8081         │        │   :9092   │        │    :8082         │
│                 │        └───────────┘        │                  │
│  POST /produce  │                             │  GET /health     │
│  GET  /health   │        ┌───────────┐        │                  │
└─────────────────┘        │ Kafka UI  │        └──────────────────┘
                           │   :8080   │
                           └───────────┘
```

The producer exposes a REST API that receives a `count` and uses a **WorkerPool** to dispatch messages concurrently to Kafka. The consumer reads from the topic and logs each message with latency metrics.

Each service has its own `go.mod` and shares no code with the other, simulating two independent repositories.

## Project Structure

```
kafka-go/
├── docker/
│   └── docker-compose.yml
├── producer/
│   ├── cmd/api/main.go
│   ├── config/config.go
│   ├── internal/
│   │   ├── controller/produce_controller.go
│   │   ├── model/message.go
│   │   ├── ports/producer_port.go
│   │   ├── router/router.go
│   │   └── service/producer_service.go
│   ├── go.mod
│   └── go.sum
└── consumer/
    ├── cmd/api/main.go
    ├── config/config.go
    ├── internal/
    │   ├── controller/health_controller.go
    │   ├── model/message.go
    │   ├── ports/consumer_port.go
    │   ├── router/router.go
    │   └── service/consumer_service.go
    ├── go.mod
    └── go.sum
```

## Prerequisites

- [Go 1.21+](https://golang.org/dl/)
- [Docker](https://www.docker.com/) + Docker Compose

## Running

### 1. Start infrastructure

```bash
docker compose -f docker/docker-compose.yml up -d
```

Wait ~30 seconds for the Kafka healthcheck to pass.

### 2. Start the consumer (terminal 1)

```bash
cd consumer
go run cmd/api/main.go
```

### 3. Start the producer (terminal 2)

```bash
cd producer
go run cmd/api/main.go
```

### 4. Send messages

```bash
curl -X POST http://localhost:8081/produce \
  -H "Content-Type: application/json" \
  -d '{"count": 50000}'
```

Expected response:

```json
{
  "total_sent": 50000,
  "total_errors": 0,
  "duration_ms": 3240
}
```

### 5. Observe

- **Consumer logs** — each message is logged with `offset`, `partition`, `seq_number`, `latency`, etc.
- **Kafka UI** — http://localhost:8080 → cluster `local` → topic `demo-topic`
- **Health endpoints:**

```bash
curl http://localhost:8081/health
# {"status":"ok","service":"producer"}

curl http://localhost:8082/health
# {"status":"ok","service":"consumer"}
```

### 6. Shut down

`Ctrl+C` in each terminal (graceful shutdown), then:

```bash
docker compose -f docker/docker-compose.yml down
```

## Configuration

All configuration is done via environment variables with sensible defaults. No `.env` file required.

### Producer

| Variable       | Default          | Description                        |
|----------------|------------------|------------------------------------|
| `KAFKA_BROKER` | `localhost:9092` | Kafka broker address               |
| `KAFKA_TOPIC`  | `demo-topic`     | Target topic                       |
| `WORKER_COUNT` | `100`            | Number of concurrent workers       |
| `BATCH_SIZE`   | `1000`           | Kafka writer batch size            |
| `SERVER_PORT`  | `8081`           | HTTP server port                   |

### Consumer

| Variable             | Default               | Description                        |
|----------------------|-----------------------|------------------------------------|
| `KAFKA_BROKER`       | `localhost:9092`      | Kafka broker address               |
| `KAFKA_TOPIC`        | `demo-topic`          | Topic to consume from              |
| `KAFKA_GROUP_ID`     | `demo-consumer-group` | Consumer group ID                  |
| `KAFKA_START_OFFSET` | `kafka.FirstOffset`   | Start offset (-2 = earliest)       |
| `SERVER_PORT`        | `8082`                | HTTP server port                   |

## Producer: WorkerPool

The producer uses a worker pool pattern to maximize throughput:

```
Dispatcher goroutine
      │
      ▼
  jobs channel
  (buffer = WORKER_COUNT × BATCH_SIZE)
      │
  ┌───┴───┐
  ▼       ▼  ... × WORKER_COUNT
worker  worker
  │       │
  └───┬───┘
      ▼
   Kafka Writer
   (Snappy compression, RequireOne acks)
```

- A single dispatcher goroutine generates all messages and pushes them into the buffered channel
- `WORKER_COUNT` goroutines drain the channel and write to Kafka concurrently
- Sent/error counts are tracked with `atomic.Int64`
- The main goroutine waits for all workers via `sync.WaitGroup`

## Message Format

Both services use the same message schema (defined independently in each app):

```go
type Message struct {
    ID        string    `json:"id"`         // UUID v4
    Payload   string    `json:"payload"`
    Timestamp time.Time `json:"timestamp"`
    Source    string    `json:"source"`     // "producer"
    SeqNumber int       `json:"seq_number"`
}
```

## Tech Stack

| Concern        | Library                                                                 |
|----------------|-------------------------------------------------------------------------|
| HTTP framework | [gin-gonic/gin](https://github.com/gin-gonic/gin)                       |
| Kafka client   | [segmentio/kafka-go](https://github.com/segmentio/kafka-go) (pure Go)  |
| UUID           | [google/uuid](https://github.com/google/uuid)                           |
| Logging        | `log/slog` (stdlib)                                                     |
| Kafka broker   | Confluent Platform 7.6.0 in KRaft mode (no Zookeeper)                  |
| Kafka UI       | [provectuslabs/kafka-ui](https://github.com/provectus/kafka-ui)         |
