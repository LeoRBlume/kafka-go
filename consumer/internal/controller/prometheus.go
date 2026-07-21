package controller

import (
	"fmt"
	"sort"
	"strings"

	"github.com/seu-usuario/kafka-go/consumer/internal/service"
)

// prometheusContentType é o content-type do text exposition format v0.0.4.
const prometheusContentType = "text/plain; version=0.0.4; charset=utf-8"

// renderPrometheus serializa uma Observation no text exposition format do
// Prometheus (counters com sufixo _total, gauges para estado).
func renderPrometheus(obs service.Observation) string {
	var b strings.Builder
	m := obs.Metrics

	counter := func(name, help string, v int64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
	}
	counter("aggregator_received_total", "Events received from the input topic.", m.Received)
	counter("aggregator_invalid_total", "Events skipped for missing id/entity_key/timestamp.", m.Invalid)
	counter("aggregator_late_events_dropped_total", "Events dropped for arriving after their window closed.", m.LateDropped)
	counter("aggregator_duplicates_total", "Events skipped as duplicates (same id within a window).", m.Duplicates)
	counter("aggregator_aggregated_total", "Events folded into a window aggregate.", m.Aggregated)
	counter("aggregator_windows_emitted_total", "Windows closed and emitted to the output topic.", m.WindowsEmitted)

	fmt.Fprintf(&b, "# HELP aggregator_open_windows Windows currently held open in state.\n"+
		"# TYPE aggregator_open_windows gauge\naggregator_open_windows %d\n", obs.OpenWindows)

	b.WriteString("# HELP aggregator_watermark_seconds Per-partition event-time watermark (unix seconds).\n" +
		"# TYPE aggregator_watermark_seconds gauge\n")
	partitions := make([]int, 0, len(obs.Watermarks))
	for p := range obs.Watermarks {
		partitions = append(partitions, p)
	}
	sort.Ints(partitions)
	for _, p := range partitions {
		fmt.Fprintf(&b, "aggregator_watermark_seconds{partition=\"%d\"} %d\n", p, obs.Watermarks[p].Unix())
	}

	return b.String()
}
