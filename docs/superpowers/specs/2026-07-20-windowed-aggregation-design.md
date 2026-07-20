# Windowed Aggregation (v1, tumbling) — Design

**Data:** 2026-07-20
**Branch:** feature/windowed-aggregation

## Objetivo

Transformar o par producer/consumer existente num pipeline de **agregação janelada
(tumbling)**: o `producer` emite eventos de negócio co-particionados por entidade, e o
`consumer` deixa de ser um mero leitor e passa a ser o **serviço de windowed aggregation**
— mantém estado por `(entity_key, window_start)`, fecha janelas por watermark + grace,
deduplica por `ID`, emite `WindowResult` num tópico de saída e recupera estado após crash.

**Decisão estrutural:** NÃO há módulo `aggregator/` novo. A lógica de agregação vive
dentro do módulo `consumer/` (mesmo path `github.com/seu-usuario/kafka-go/consumer`).

## Decisões travadas

- **EntityKey (producer):** pool configurável `EntityKeyCount` (default 10) →
  `card_0..card_9`. `Value` = float aleatório. `ID` continua UUID gerado na origem
  (satisfaz unicidade/idempotência). **Key Kafka muda de `msg.ID` → `[]byte(EntityKey)`.**
- **Consumer:** vira o agregador (substitui o loop de leitura+log).
- **Testes:** apenas os dois críticos do aceite — idempotência por `ID` e crash-recovery
  idêntico — com `Clock` injetável e state store em disco temporário. `go test -race`.

## Garantias da v1

At-least-once + idempotência por `ID`. **Fora de escopo:** sliding/session windows,
exactly-once transacional, changelog topic como state store, banco externo.

## Partes de entrega (cada uma compila isolada)

### Parte 1 — Producer (input event generator)
- `model.Message` → `model.Event` (`ID, EntityKey, Value, Timestamp, Source, SeqNumber`).
- Config: `EntityKeyCount` (default 10).
- `Produce`: `EntityKey = card_<seq % EntityKeyCount>`, `Value` aleatório,
  `kafka.Message{Key: []byte(EntityKey), Value: json}`.

### Parte 2 — Consumer: contrato + modelo + config
- `internal/model/event.go`: `Event`, `WindowResult`, `WindowState`.
- `internal/ports/`: `AggregatorPort` (Start/Close), `StateStorePort`, `Clock`.
- `config/config.go` estende: `WindowSize=5m`, `GracePeriod=1m`, `OutputTopic` (obrigatório),
  `SnapshotInterval=30s`, `StateDir=./state`.

### Parte 3 — StateStore em disco (`internal/service/statestore.go`)
- `Get/Set/Delete` sobre `map[key]*WindowState` (key = `entityKey + "|" + RFC3339 UTC`).
- `Snapshot(offsets)`: grava estado+offsets em arquivo temp + `os.Rename` (atômico).
- `Restore()`: recarrega estado e devolve `map[partition]offset`.

### Parte 4 — Aggregator core (`internal/service/aggregator_service.go`)
- `Start`: `Restore()` → um `kafka.Reader` por partição atribuída com `SetOffset` no offset
  restaurado → 1 goroutine por partição usando `FetchMessage` + `CommitMessages`
  (sem auto-commit).
- Por msg: unmarshal → valida `ID/EntityKey/Timestamp` (inválido: warn+skip+métrica) →
  bucket `Timestamp.Truncate(WindowSize)` UTC → dedup `SeenIDs` → atualiza `Count/Sum` →
  avança watermark da partição.
- Ticker de fechamento: janela com `watermark > windowEnd + grace` → emite `WindowResult`
  (writer `RequireAll`) → `Delete` do estado.
- Late event (bucket de janela já fechada): descarta, `Warnf` com `id/entity_key/atraso`,
  incrementa `late_events_dropped`.
- Ticker de snapshot: `Snapshot(estado+offsets)` **e só então** `CommitMessages`.
- `ctx.Done()`: snapshot final + commit, depois `Close`.

### Parte 5 — Wiring + controller
- `main.go` liga o agregador; `/health` + `/metrics` (contadores atômicos:
  processed, invalid, duplicates, late_events_dropped, windows_emitted).

### Parte 6 — Testes essenciais
- Idempotência: mesmo `ID` N vezes → `Count/Sum` inalterados.
- Crash-recovery: processa metade → `Snapshot` → nova instância `Restore` → processa resto
  → `WindowResult` idêntico ao run sem crash. `Clock` fake, sem `time.Sleep`.

### Parte 7 — Infra
- `consumer/Dockerfile` (já existe) revisado; `docker-compose` com `OUTPUT_TOPIC` +
  volume para `StateDir`; manifests k8s espelhando o padrão atual.

## Concorrência

Cada partição atribuída = 1 reader + 1 goroutine + sua fatia de estado. Sem locks
compartilhados de estado entre partições (single writer por chave). O `kafka.Writer` de
saída é compartilhado → protegido por mutex. Contadores de métrica via `atomic`.

## Regra de ouro

Commit de offset **sempre depois** do snapshot bem-sucedido. Nunca auto-commit por tempo.

## Contrato dos tipos

```go
type Event struct {
    ID        string    `json:"id"`
    EntityKey string    `json:"entity_key"`
    Value     float64   `json:"value"`
    Timestamp time.Time `json:"timestamp"`
    Source    string    `json:"source"`
    SeqNumber int       `json:"seq_number"`
}

type WindowResult struct {
    EntityKey   string    `json:"entity_key"`
    WindowStart time.Time `json:"window_start"`
    WindowEnd   time.Time `json:"window_end"`
    Count       int64     `json:"count"`
    Sum         float64   `json:"sum"`
    Avg         float64   `json:"avg"`
    EmittedAt   time.Time `json:"emitted_at"`
}

type WindowState struct {
    Count   int64
    Sum     float64
    SeenIDs map[string]struct{}
}
```

## Critérios de aceite

- 1 `WindowResult` por `(EntityKey, WindowStart)` com `Count = N` após `windowEnd + grace`.
- Mesmo `ID` repetido não altera `Count/Sum`.
- Crash no meio da janela + restart → resultado idêntico ao cenário sem crash.
- Evento anterior a janela fechada → descartado + `late_events_dropped++`.
- `go test -race` passa; nenhuma partição compartilha estado mutável.
- Logs no padrão `logger.Infof/Error` com contexto e nome do método.
