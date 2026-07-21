package model

// WindowState é o agregado mutável mantido em memória para cada
// (entity_key, window_start) enquanto a janela está aberta. SeenIDs deduplica
// eventos por ID dentro da janela, garantindo idempotência sob replay.
type WindowState struct {
	Count   int64               `json:"count"`
	Sum     float64             `json:"sum"`
	SeenIDs map[string]struct{} `json:"seen_ids"`
}
