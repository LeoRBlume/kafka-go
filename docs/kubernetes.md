# Kubernetes — Estrutura e Explicação

Toda a infraestrutura local roda em um cluster [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker), gerenciada via [Kustomize](https://kustomize.io/).

---

## Estrutura de pastas

```
k8s/
├── kind-config.yaml       # Configuração do cluster kind
└── base/
    ├── kustomization.yaml # Raiz do Kustomize — agrupa kafka + apps
    ├── kafka/             # Tudo relacionado ao broker Kafka
    │   ├── kustomization.yaml
    │   ├── namespace.yaml
    │   ├── statefulset.yaml
    │   └── service.yaml
    └── apps/              # Producer, Consumer e Kafka UI
        ├── kustomization.yaml
        ├── namespace.yaml
        ├── producer.yaml
        ├── consumer.yaml
        └── kafka-ui.yaml
```

---

## kind-config.yaml

Define o cluster local criado pelo kind. O ponto mais importante são os `extraPortMappings`: mapeiam portas do nó Kubernetes para o host (sua máquina), permitindo acesso direto via `localhost` sem port-forward.

```
NodePort 30080  →  localhost:8080  (Kafka UI)
NodePort 30081  →  localhost:8081  (Producer)
NodePort 30082  →  localhost:8082  (Consumer)
```

Sem esse mapeamento, os serviços NodePort só seriam acessíveis de dentro do cluster.

---

## base/kustomization.yaml

Ponto de entrada do Kustomize. Agrupa os dois módulos (`kafka` e `apps`) para que um único comando aplique tudo:

```bash
kubectl apply -k k8s/base
```

---

## Namespace `kafka`

Isola o broker Kafka de todo o resto. Recursos dentro desse namespace só são acessados por outros pods via DNS completo: `kafka.kafka.svc.cluster.local`.

---

## kafka/statefulset.yaml

Roda o broker Kafka como **StatefulSet** (e não Deployment) porque:
- Mantém identidade estável do pod (`kafka-0`)
- Garante que o volume de dados persiste entre restarts
- Necessário para o Kafka saber seu próprio endereço

**Configurações importantes:**

| Campo | Valor | Por quê |
|-------|-------|---------|
| `enableServiceLinks: false` | desabilitado | O K8s injeta automaticamente variáveis de ambiente para cada Service no namespace. Como o Service se chama `kafka`, ele injetaria `KAFKA_PORT=tcp://...` — que conflita diretamente com as variáveis de configuração do Confluent (`KAFKA_*`). Desabilitar evita esse conflito. |
| `KAFKA_PROCESS_ROLES` | `broker,controller` | Modo KRaft: o Kafka roda sem Zookeeper. O mesmo pod age como broker (recebe mensagens) e controller (gerencia metadados). |
| `KAFKA_CONTROLLER_QUORUM_VOTERS` | `1@localhost:9093` | Em cluster single-node no K8s, o controller precisa referenciar a si mesmo via `localhost`. Usar o DNS do pod causaria falha de resolução durante o boot. |
| `KAFKA_ADVERTISED_LISTENERS` | `PLAINTEXT://kafka.kafka.svc.cluster.local:9092` | Endereço que o Kafka anuncia para os clientes. Producer e consumer usam exatamente esse endereço para conectar. |
| `readinessProbe` (tcpSocket) | porta 9092 | Verifica se o Kafka está aceitando conexões TCP. Não usa `kafka-topics --list` porque o JVM demora mais de 30s para iniciar, causando timeout da probe. |
| `volumeClaimTemplates` | 1Gi | Solicita um PersistentVolume para armazenar os dados do Kafka. O StatefulSet cria um PVC automaticamente (`kafka-data-kafka-0`). |

---

## kafka/service.yaml

Service **headless** (`clusterIP: None`) para o Kafka.

Um Service headless não tem IP virtual — o DNS retorna diretamente o IP do pod. Isso é necessário porque:
1. O StatefulSet precisa que cada pod tenha um DNS estável e previsível
2. O pod `kafka-0` fica acessível como `kafka-0.kafka.kafka.svc.cluster.local`
3. Clientes que precisam se conectar a um broker específico (e não a um load balancer) funcionam corretamente

---

## Namespace `apps`

Isola os serviços de aplicação (producer, consumer, kafka-ui) do broker. Comunicação com o Kafka sempre via DNS cross-namespace: `kafka.kafka.svc.cluster.local:9092`.

---

## apps/producer.yaml

**Deployment** com 1 réplica + **Service NodePort**.

O producer é stateless — não mantém estado, qualquer instância pode receber requisições. Por isso usa Deployment (e não StatefulSet).

Configurações relevantes:

| Env var | Valor | Descrição |
|---------|-------|-----------|
| `WORKER_COUNT` | `5000` | Goroutines paralelas enviando mensagens ao Kafka |
| `BATCH_SIZE` | `100` | Tamanho do batch interno do Kafka writer |
| `imagePullPolicy: Never` | — | Usa a imagem carregada localmente no kind via `kind load docker-image`. Sem isso, o K8s tentaria fazer pull do Docker Hub e falharia. |

O `readinessProbe` faz GET em `/health` antes de considerar o pod pronto para receber tráfego.

---

## apps/consumer.yaml

**Deployment** com 50 réplicas + **Service NodePort**.

Todos os 50 pods fazem parte do mesmo consumer group (`demo-consumer-group`). O Kafka distribui as 50 partições do tópico `demo-topic` entre os pods — cada pod recebe exatamente 1 partição.

> **Regra do Kafka:** número de consumers ativos em um grupo = número de partições do tópico. Consumers a mais ficam idle.

Por isso o tópico é criado com 50 partições no `deploy.sh`.

---

## apps/kafka-ui.yaml

**Deployment** com 1 réplica + **Service NodePort**.

Interface web ([provectuslabs/kafka-ui](https://github.com/provectus/kafka-ui)) que se conecta ao broker via `kafka.kafka.svc.cluster.local:9092`. Permite visualizar tópicos, mensagens, consumer groups, lag e configurações do cluster.

Acessível em **http://localhost:8080**.

O `readinessProbe` usa `/actuator/health` (endpoint do Spring Boot Actuator embutido na imagem) com `initialDelaySeconds: 15` para dar tempo ao JVM inicializar.

---

## Kustomize

Kustomize é uma ferramenta nativa do kubectl para gerenciar manifestos Kubernetes sem templates. Cada pasta tem um `kustomization.yaml` listando os arquivos que fazem parte daquele módulo.

A hierarquia funciona assim:

```
k8s/base/kustomization.yaml        ← aplica tudo
    └── k8s/base/kafka/            ← namespace + statefulset + service
    └── k8s/base/apps/             ← namespace + producer + consumer + kafka-ui
```

Aplicar tudo de uma vez:
```bash
kubectl apply -k k8s/base
```

Inspecionar o que seria gerado sem aplicar:
```bash
kubectl kustomize k8s/base
```

---

## Fluxo de rede dentro do cluster

```
curl localhost:8081/produce
        │
        ▼
  NodePort 30081
        │
        ▼
  producer (apps namespace)
        │  kafka.kafka.svc.cluster.local:9092
        ▼
  kafka-0 (kafka namespace)
        │  kafka.kafka.svc.cluster.local:9092
        ▼
  consumer-* x50 (apps namespace)
```

---

## Por que StatefulSet para o Kafka e Deployment para o resto?

| Característica | StatefulSet | Deployment |
|---------------|-------------|------------|
| Identidade do pod | Estável (`kafka-0`) | Aleatória (`kafka-xyz123`) |
| Volume por pod | Sim (PVC dedicado) | Não (compartilhado ou efêmero) |
| Ordem de inicialização | Garantida | Sem garantia |
| Caso de uso | Bancos de dados, brokers | APIs stateless |

O Kafka precisa de identidade estável porque armazena dados em disco e precisa saber seu próprio endereço para anunciar aos clientes. Producer, consumer e kafka-ui não guardam estado — um pod novo é idêntico ao anterior.
