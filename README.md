# kafka-go

Dois microsserviços Go independentes comunicando via Apache Kafka, arquitetura ports & adapters, rodando em Kubernetes local com kind.

---

## Visão geral

```mermaid
graph LR
    Client(["👤 Client"])
    subgraph K8s["Kubernetes (kind)"]
        subgraph apps["namespace: apps"]
            P["Producer\n:8081"]
            C1["Consumer 1\n:8082"]
            C2["Consumer 2\n:8082"]
            C3["Consumer ..50\n:8082"]
            UI["Kafka UI\n:8080"]
        end
        subgraph kafka["namespace: kafka"]
            K[("Kafka\n:9092\n50 partições")]
        end
    end

    Client -->|"POST /produce"| P
    P -->|"publica mensagens"| K
    K -->|"partição 0..16"| C1
    K -->|"partição 17..33"| C2
    K -->|"partição 34..49"| C3
    UI -->|"monitora"| K
```

---

## Arquitetura: Ports & Adapters

Cada serviço é estruturado em camadas — a lógica de negócio não depende de frameworks ou do Kafka diretamente.

```mermaid
graph TB
    subgraph Producer
        direction TB
        HTTP["HTTP Handler\nGin Controller"]
        PP["Port\nProducerPort"]
        PS["Service\nWorker Pool"]
        KW["Kafka Writer\nsegmentio/kafka-go"]

        HTTP --> PP
        PP --> PS
        PS --> KW
    end

    subgraph Consumer
        direction TB
        KR["Kafka Reader\nsegmentio/kafka-go"]
        CP["Port\nConsumerPort"]
        CS["Service\nReadMessage loop"]
        LOG["Logger\ngo-libs/logger"]

        KR --> CP
        CP --> CS
        CS --> LOG
    end
```

---

## Producer: Worker Pool

O producer usa um worker pool para maximizar o throughput ao publicar mensagens no Kafka.

```mermaid
flowchart TD
    REQ["POST /produce\n{ count: N }"]
    DISP["Dispatcher goroutine\ngera N mensagens"]
    CH["jobs channel\nbuffer = WORKER_COUNT × BATCH_SIZE"]
    W1["Worker 1"]
    W2["Worker 2"]
    WN["Worker ...5000"]
    KW[("Kafka Writer\nSnappy + RequireOne")]
    RES["ProduceResult\n{ sent, errors, duration_ms }"]

    REQ --> DISP
    DISP -->|"kafka.Message"| CH
    CH --> W1
    CH --> W2
    CH --> WN
    W1 --> KW
    W2 --> KW
    WN --> KW
    KW --> RES
```

- **5000 workers** consomem o channel em paralelo
- **Buffer** do channel = `WORKER_COUNT × BATCH_SIZE` para o dispatcher raramente bloquear
- Contagem de enviados/erros via `atomic.Int64`
- Workers sincronizados com `sync.WaitGroup`

---

## Consumer: Read Loop

```mermaid
flowchart TD
    START(["consumer.Start(ctx)"])
    READ["reader.ReadMessage(ctx)"]
    ERR{"erro?"}
    CTX{"ctx cancelado?"}
    BACKOFF["aguarda 2s\nou ctx.Done()"]
    UNMARSHAL["json.Unmarshal"]
    UERR{"falha unmarshal?"}
    LOG["logger.Infof\nid, seq, partition, offset, latency"]
    STOP(["return nil"])

    START --> READ
    READ --> ERR
    ERR -->|"sim"| CTX
    CTX -->|"cancelado/deadline"| STOP
    CTX -->|"outro erro"| BACKOFF
    BACKOFF --> READ
    ERR -->|"não"| UNMARSHAL
    UNMARSHAL --> UERR
    UERR -->|"sim"| READ
    UERR -->|"não"| LOG
    LOG --> READ
```

- Cada pod consome **1 partição** do tópico
- Backoff de **2 segundos** em caso de erro de leitura
- `context.Canceled` e `context.DeadlineExceeded` encerram o loop graciosamente

---

## Fluxo de rede no Kubernetes

```mermaid
graph LR
    HOST["localhost"]

    HOST -->|":8080"| NP80["NodePort 30080"]
    HOST -->|":8081"| NP81["NodePort 30081"]
    HOST -->|":8082"| NP82["NodePort 30082"]

    subgraph cluster["kind cluster"]
        NP80 --> UI["kafka-ui pod\n(apps)"]
        NP81 --> PR["producer pod\n(apps)"]
        NP82 --> CN["consumer pods x50\n(apps)"]

        PR -->|"kafka.kafka.svc.cluster.local:9092"| KF["kafka-0\n(kafka)"]
        CN -->|"kafka.kafka.svc.cluster.local:9092"| KF
        UI -->|"kafka.kafka.svc.cluster.local:9092"| KF
    end
```

---

## Subir o ambiente

**Pré-requisitos:** Docker Desktop, kind, kubectl, Go 1.26+

```bash
bash scripts/deploy.sh
```

O script cria o cluster, builda as imagens, aplica os manifestos, cria o tópico com 50 partições e reinicia os consumers.

---

## Endpoints

| Serviço | URL |
|---------|-----|
| Producer API | http://localhost:8081 |
| Consumer health | http://localhost:8082/health |
| Kafka UI | http://localhost:8080 |

---

## Curls

```bash
# Produzir 10.000 mensagens
curl -s -X POST http://localhost:8081/produce \
  -H 'Content-Type: application/json' \
  -d '{"count": 10000}'

# Resposta
{"total_sent":10000,"total_errors":0,"duration_ms":163}

# Health checks
curl http://localhost:8081/health
curl http://localhost:8082/health
```

---

## Formato da mensagem

```go
type Message struct {
    ID        string    `json:"id"`         // UUID v4
    Payload   string    `json:"payload"`
    Timestamp time.Time `json:"timestamp"`  // usado para calcular latência
    Source    string    `json:"source"`     // "producer"
    SeqNumber int       `json:"seq_number"`
}
```

---

## Configuração

### Producer

| Variável | Padrão | Descrição |
|----------|--------|-----------|
| `KAFKA_BROKER` | `localhost:9092` | Endereço do broker |
| `KAFKA_TOPIC` | `demo-topic` | Tópico de destino |
| `WORKER_COUNT` | `5000` | Goroutines paralelas |
| `BATCH_SIZE` | `100` | Batch size do Kafka writer |
| `SERVER_PORT` | `8081` | Porta HTTP |

### Consumer

| Variável | Padrão | Descrição |
|----------|--------|-----------|
| `KAFKA_BROKER` | `localhost:9092` | Endereço do broker |
| `KAFKA_TOPIC` | `demo-topic` | Tópico a consumir |
| `KAFKA_GROUP_ID` | `demo-consumer-group` | Consumer group ID |
| `KAFKA_START_OFFSET` | `-2` (earliest) | Offset inicial |
| `SERVER_PORT` | `8082` | Porta HTTP |

---

## Estrutura do projeto

```
kafka-go/
├── producer/                        # Microsserviço producer (porta 8081)
│   ├── cmd/api/main.go              # Entrypoint, setup do logger e HTTP server
│   ├── config/config.go             # Leitura de env vars
│   ├── internal/
│   │   ├── controller/              # Handler HTTP — valida request, chama service
│   │   ├── model/message.go         # Struct de mensagem
│   │   ├── ports/producer_port.go   # Interface ProducerPort
│   │   ├── router/router.go         # Registro de rotas Gin
│   │   └── service/producer_service.go  # Worker pool + Kafka writer
│   └── Dockerfile
├── consumer/                        # Microsserviço consumer (porta 8082)
│   ├── cmd/api/main.go              # Entrypoint, goroutine de consume + HTTP server
│   ├── config/config.go             # Leitura de env vars
│   ├── internal/
│   │   ├── controller/              # Handler HTTP — apenas /health
│   │   ├── model/message.go         # Struct de mensagem
│   │   ├── ports/consumer_port.go   # Interface ConsumerPort
│   │   ├── router/router.go         # Registro de rotas Gin
│   │   └── service/consumer_service.go  # Loop ReadMessage + backoff
│   └── Dockerfile
├── k8s/
│   ├── kind-config.yaml             # Cluster kind com port mappings
│   └── base/
│       ├── kustomization.yaml       # Raiz Kustomize
│       ├── kafka/                   # StatefulSet + Service headless
│       └── apps/                    # Producer + Consumer (x50) + Kafka UI
├── docker/
│   └── docker-compose.yml           # Ambiente local sem K8s
├── scripts/
│   └── deploy.sh                    # Deploy completo no kind
└── docs/
    ├── runbook.md                   # Como rodar, logs, curls, comandos
    └── kubernetes.md                # Explicação de cada arquivo K8s
```

---

## Tech stack

| Concern | Lib |
|---------|-----|
| HTTP | [gin-gonic/gin](https://github.com/gin-gonic/gin) |
| Kafka client | [segmentio/kafka-go](https://github.com/segmentio/kafka-go) |
| UUID | [google/uuid](https://github.com/google/uuid) |
| Logger | [LeoRBlume/go-libs/logger](https://github.com/LeoRBlume/go-libs) |
| Kafka broker | Confluent Platform 7.6.1 — KRaft (sem Zookeeper) |
| Kafka UI | [provectuslabs/kafka-ui](https://github.com/provectus/kafka-ui) |
| Kubernetes local | [kind](https://kind.sigs.k8s.io/) + Kustomize |
