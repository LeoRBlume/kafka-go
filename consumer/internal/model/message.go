package model

import "time"

type Message struct {
	ID        string    `json:"id"`
	Payload   string    `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	SeqNumber int       `json:"seq_number"`
}
