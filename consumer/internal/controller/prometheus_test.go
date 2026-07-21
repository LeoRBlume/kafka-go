package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/seu-usuario/kafka-go/consumer/internal/service"
)

func TestRenderPrometheus(t *testing.T) {
	obs := service.Observation{
		Metrics: service.MetricsSnapshot{
			Received: 100, Invalid: 2, LateDropped: 1,
			Duplicates: 5, Aggregated: 92, WindowsEmitted: 7,
		},
		OpenWindows: 3,
		Watermarks: map[int]time.Time{
			7: time.Unix(1_750_000_000, 0),
			0: time.Unix(1_750_000_010, 0),
		},
	}

	out := renderPrometheus(obs)

	wantLines := []string{
		"# TYPE aggregator_received_total counter",
		"aggregator_received_total 100",
		"aggregator_late_events_dropped_total 1",
		"# TYPE aggregator_open_windows gauge",
		"aggregator_open_windows 3",
		"# TYPE aggregator_watermark_seconds gauge",
		`aggregator_watermark_seconds{partition="0"} 1750000010`,
		`aggregator_watermark_seconds{partition="7"} 1750000000`,
	}
	for _, l := range wantLines {
		if !strings.Contains(out, l) {
			t.Errorf("output missing line:\n%s\n--- full output ---\n%s", l, out)
		}
	}

	// Partições devem sair ordenadas (0 antes de 7) para saída estável.
	if strings.Index(out, `partition="0"`) > strings.Index(out, `partition="7"`) {
		t.Errorf("partitions not sorted:\n%s", out)
	}
}
