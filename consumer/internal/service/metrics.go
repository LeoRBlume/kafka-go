package service

import (
	"sync/atomic"
	"time"
)

// Metrics são os contadores observáveis do agregador (thread-safe via atomic).
type Metrics struct {
	Received   atomic.Int64
	Invalid    atomic.Int64
	Late       atomic.Int64
	Duplicates atomic.Int64
	Aggregated atomic.Int64
	Emitted    atomic.Int64
}

// MetricsSnapshot é uma leitura pontual dos contadores, exposta pelo controller.
type MetricsSnapshot struct {
	Received       int64 `json:"received"`
	Invalid        int64 `json:"invalid"`
	LateDropped    int64 `json:"late_events_dropped"`
	Duplicates     int64 `json:"duplicates"`
	Aggregated     int64 `json:"aggregated"`
	WindowsEmitted int64 `json:"windows_emitted"`
}

func (m *Metrics) snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Received:       m.Received.Load(),
		Invalid:        m.Invalid.Load(),
		LateDropped:    m.Late.Load(),
		Duplicates:     m.Duplicates.Load(),
		Aggregated:     m.Aggregated.Load(),
		WindowsEmitted: m.Emitted.Load(),
	}
}

// Observation é uma leitura pontual de tudo que o /metrics expõe: os contadores
// mais os gauges de estado (janelas abertas e watermark por partição).
type Observation struct {
	Metrics     MetricsSnapshot
	OpenWindows int
	Watermarks  map[int]time.Time // event-time watermark por partição
}
