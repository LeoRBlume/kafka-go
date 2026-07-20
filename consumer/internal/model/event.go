package model

import "time"

// Event é o evento de negócio lido do tópico de entrada.
// O ID é a chave de idempotência; o EntityKey é a chave de negócio/partição;
// o Timestamp é o event time que define a janela.
type Event struct {
	ID        string    `json:"id"`
	EntityKey string    `json:"entity_key"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	SeqNumber int       `json:"seq_number"`
}

// WindowResult é o resultado de uma janela tumbling fechada, publicado no
// tópico de saída. A chave da mensagem de saída é o EntityKey; o consumidor
// downstream deve tratar por upsert em (entity_key, window_start).
type WindowResult struct {
	EntityKey   string    `json:"entity_key"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	Count       int64     `json:"count"`
	Sum         float64   `json:"sum"`
	Avg         float64   `json:"avg"`
	EmittedAt   time.Time `json:"emitted_at"`
}

// WindowState é o agregado mutável mantido em memória para cada
// (entity_key, window_start) enquanto a janela está aberta. SeenIDs deduplica
// eventos por ID dentro da janela, garantindo idempotência sob replay.
type WindowState struct {
	Count   int64               `json:"count"`
	Sum     float64             `json:"sum"`
	SeenIDs map[string]struct{} `json:"seen_ids"`
}
