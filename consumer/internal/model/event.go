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
