package model

import "time"

// Event é o evento de negócio publicado no tópico de entrada.
// A chave Kafka da mensagem é o EntityKey (co-particionamento por entidade),
// e o ID é a chave de idempotência (único e imutável desde a origem).
type Event struct {
	ID        string    `json:"id"`         // único e determinístico, gerado na origem
	EntityKey string    `json:"entity_key"` // chave de negócio / partição (ex: card_1)
	Value     float64   `json:"value"`      // valor a agregar
	Timestamp time.Time `json:"timestamp"`  // event time (define a janela)
	Source    string    `json:"source"`
	SeqNumber int       `json:"seq_number"`
}
