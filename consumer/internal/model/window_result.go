package model

import "time"

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
