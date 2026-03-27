# kafka-go — Runbook

Dois microsserviços Go (producer + consumer) comunicando via Apache Kafka, rodando em Kubernetes local com kind.

---

## Pré-requisitos

- [Docker Desktop](https://www.docker.com/products/docker-desktop/)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Go 1.26+](https://go.dev/dl/)

---

## Subir o ambiente

```bash
bash scripts/deploy.sh
```

O script:
1. Cria o cluster kind `kafka-go` (se não existir)
2. Builda as imagens Docker do producer e consumer
3. Carrega as imagens no kind
4. Aplica todos os manifestos Kubernetes
5. Aguarda o Kafka ficar ready
6. Cria o tópico `demo-topic` com 50 partições
7. Reinicia os consumers para garantir atribuição de partições (1 por pod)

---

## Endpoints

| Serviço      | URL                          |
|--------------|------------------------------|
| Producer API | http://localhost:8081        |
| Consumer health | http://localhost:8082/health |
| Kafka UI     | http://localhost:8080        |

---

## Kafka UI

Acesse **http://localhost:8080** para visualizar:
- Tópicos e mensagens
- Consumer groups e lag
- Brokers e configurações

Cluster configurado: `k8s` → `kafka.kafka.svc.cluster.local:9092`

---

## Curls

### Produzir mensagens

```bash
# Produzir 100 mensagens
curl -s -X POST http://localhost:8081/produce \
  -H 'Content-Type: application/json' \
  -d '{"count": 100}'

# Produzir 10.000 mensagens
curl -s -X POST http://localhost:8081/produce \
  -H 'Content-Type: application/json' \
  -d '{"count": 10000}'
```

Resposta:
```json
{"total_sent":10000,"total_errors":0,"duration_ms":163}
```

### Health checks

```bash
curl http://localhost:8081/health
curl http://localhost:8082/health
```

---

## Logs dos pods

### Producer

```bash
# Logs em tempo real
kubectl logs -n apps deployment/producer -f

# Últimas 50 linhas
kubectl logs -n apps deployment/producer --tail=50
```

### Consumer (50 réplicas)

```bash
# Logs de todos os pods do consumer em tempo real
kubectl logs -n apps -l app=consumer -f

# Logs de um pod específico
kubectl logs -n apps <nome-do-pod> -f

# Ver apenas mensagens recebidas
kubectl logs -n apps -l app=consumer | grep "message received"
```

### Kafka UI

```bash
kubectl logs -n apps deployment/kafka-ui --tail=30
```

### Kafka

```bash
kubectl logs -n kafka kafka-0 --tail=50
```

---

## Status dos pods

```bash
# Todos os pods
kubectl get pods -n kafka
kubectl get pods -n apps

# Detalhes de um pod
kubectl describe pod -n apps <nome-do-pod>
```

---

## Comandos Kafka (via kubectl exec)

```bash
# Listar tópicos
kubectl exec -n kafka kafka-0 -- \
  kafka-topics --bootstrap-server localhost:9092 --list

# Detalhes do tópico
kubectl exec -n kafka kafka-0 -- \
  kafka-topics --bootstrap-server localhost:9092 \
  --describe --topic demo-topic

# Total de mensagens no tópico (offset atual)
kubectl exec -n kafka kafka-0 -- \
  kafka-run-class kafka.tools.GetOffsetShell \
  --broker-list localhost:9092 --topic demo-topic

# Consumer group — lag e offsets
kubectl exec -n kafka kafka-0 -- \
  kafka-consumer-groups --bootstrap-server localhost:9092 \
  --group demo-consumer-group --describe

# Consumer group — membros e partições atribuídas
kubectl exec -n kafka kafka-0 -- \
  kafka-consumer-groups --bootstrap-server localhost:9092 \
  --group demo-consumer-group --describe --members

# Ler mensagens diretamente do tópico
kubectl exec -n kafka kafka-0 -- \
  kafka-console-consumer --bootstrap-server localhost:9092 \
  --topic demo-topic --from-beginning --max-messages 10
```

---

## Configuração do producer (env vars)

| Variável       | Padrão                               | Descrição                  |
|----------------|--------------------------------------|----------------------------|
| `KAFKA_BROKER` | `localhost:9092`                     | Endereço do broker         |
| `KAFKA_TOPIC`  | `demo-topic`                         | Tópico de destino          |
| `SERVER_PORT`  | `8081`                               | Porta HTTP                 |
| `WORKER_COUNT` | `5000`                               | Goroutines paralelas       |
| `BATCH_SIZE`   | `100`                                | Tamanho do batch Kafka     |

Configurado em: `k8s/base/apps/producer.yaml`

## Configuração do consumer (env vars)

| Variável              | Padrão                | Descrição                     |
|-----------------------|-----------------------|-------------------------------|
| `KAFKA_BROKER`        | `localhost:9092`      | Endereço do broker            |
| `KAFKA_TOPIC`         | `demo-topic`          | Tópico a consumir             |
| `KAFKA_GROUP_ID`      | `demo-consumer-group` | Consumer group ID             |
| `SERVER_PORT`         | `8082`                | Porta HTTP                    |

Configurado em: `k8s/base/apps/consumer.yaml` — **50 réplicas**, 1 partição por pod.

---

## Derrubar o ambiente

```bash
# Deletar o cluster (remove tudo)
kind delete cluster --name kafka-go
```

---

## Estrutura do projeto

```
kafka-go/
├── producer/               # Microsserviço producer (porta 8081)
│   ├── cmd/api/main.go
│   ├── config/
│   ├── internal/
│   │   ├── controller/     # Handler HTTP POST /produce
│   │   ├── model/
│   │   ├── ports/
│   │   ├── router/
│   │   └── service/        # Worker pool + Kafka writer
│   └── Dockerfile
├── consumer/               # Microsserviço consumer (porta 8082)
│   ├── cmd/api/main.go
│   ├── config/
│   ├── internal/
│   │   ├── controller/     # Handler HTTP GET /health
│   │   ├── model/
│   │   ├── ports/
│   │   ├── router/
│   │   └── service/        # Loop ReadMessage + backoff
│   └── Dockerfile
├── k8s/
│   ├── kind-config.yaml    # Configuração do cluster kind (port mappings)
│   └── base/
│       ├── kafka/          # StatefulSet + Service headless
│       └── apps/           # Producer + Consumer + Kafka UI
├── docker/
│   └── docker-compose.yml  # Ambiente local sem K8s
└── scripts/
    └── deploy.sh           # Script de deploy completo
```
