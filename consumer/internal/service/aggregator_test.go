package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/LeoRBlume/go-libs/logger"
	"github.com/seu-usuario/kafka-go/consumer/internal/model"
	"github.com/seu-usuario/kafka-go/consumer/internal/ports"
)

func TestMain(m *testing.M) {
	logger.Setup(logger.Config{ServiceName: "aggregator-test", Level: logger.LevelError})
	os.Exit(m.Run())
}

// fakeClock é um relógio fixo para tornar EmittedAt determinístico nos testes.
type fakeClock struct{ t time.Time }

func (c fakeClock) Now() time.Time { return c.t }

const (
	windowSize = time.Minute
	grace      = 10 * time.Second
)

var baseTime = time.Date(2026, 7, 20, 15, 0, 0, 0, time.UTC)

func newTestStore(t *testing.T, dir string) ports.StateStorePort {
	t.Helper()
	store, err := NewDiskStateStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStateStore: %v", err)
	}
	return store
}

func captureEmitter() (emitFunc, *[]model.WindowResult) {
	var out []model.WindowResult
	emit := func(_ context.Context, res model.WindowResult) error {
		out = append(out, res)
		return nil
	}
	return emit, &out
}

// makeEvents cria n eventos distintos na mesma janela (base) para a mesma entidade.
func makeEvents(entityKey string, n int) []model.Event {
	evs := make([]model.Event, n)
	for i := 0; i < n; i++ {
		evs[i] = model.Event{
			ID:        fmt.Sprintf("%s-e%d", entityKey, i),
			EntityKey: entityKey,
			Value:     float64(i),
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
		}
	}
	return evs
}

// triggerEvent avança o watermark para muito depois da janela base, forçando o
// fechamento das janelas de baseTime. Usa uma entidade separada.
func triggerEvent() model.Event {
	return model.Event{
		ID:        "trigger",
		EntityKey: "card_trigger",
		Value:     0,
		Timestamp: baseTime.Add(10 * time.Minute),
	}
}

// TestProcessEvent_DedupByID: o mesmo ID repetido não altera Count/Sum.
func TestProcessEvent_DedupByID(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, t.TempDir())
	agg := newAggregator(store, fakeClock{baseTime}, windowSize, grace, func(context.Context, model.WindowResult) error { return nil })

	ev := model.Event{ID: "dup", EntityKey: "card_1", Value: 5, Timestamp: baseTime.Add(5 * time.Second)}
	for i := 0; i < 4; i++ {
		if err := agg.processEvent(ctx, 0, ev); err != nil {
			t.Fatalf("processEvent: %v", err)
		}
	}

	st, found, err := store.Get("card_1", baseTime)
	if err != nil || !found {
		t.Fatalf("expected window state, found=%t err=%v", found, err)
	}
	if st.Count != 1 {
		t.Errorf("Count = %d, want 1", st.Count)
	}
	if st.Sum != 5 {
		t.Errorf("Sum = %v, want 5", st.Sum)
	}
	if got := agg.metrics.Duplicates.Load(); got != 3 {
		t.Errorf("Duplicates = %d, want 3", got)
	}
	if got := agg.metrics.Aggregated.Load(); got != 1 {
		t.Errorf("Aggregated = %d, want 1", got)
	}
}

// TestProcessEvent_LateDropped: evento de janela já fechada é descartado.
func TestProcessEvent_LateDropped(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, t.TempDir())
	agg := newAggregator(store, fakeClock{baseTime}, windowSize, grace, func(context.Context, model.WindowResult) error { return nil })

	// Avança o watermark para muito além da janela base.
	if err := agg.processEvent(ctx, 0, triggerEvent()); err != nil {
		t.Fatalf("processEvent trigger: %v", err)
	}

	late := model.Event{ID: "late", EntityKey: "card_1", Value: 99, Timestamp: baseTime.Add(1 * time.Second)}
	if err := agg.processEvent(ctx, 0, late); err != nil {
		t.Fatalf("processEvent late: %v", err)
	}

	if got := agg.metrics.Late.Load(); got != 1 {
		t.Errorf("LateDropped = %d, want 1", got)
	}
	if _, found, _ := store.Get("card_1", baseTime); found {
		t.Errorf("late event created window state, want none")
	}
}

// TestProcessEvent_InvalidSkipped: eventos sem ID/EntityKey/Timestamp são pulados.
func TestProcessEvent_InvalidSkipped(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, t.TempDir())
	agg := newAggregator(store, fakeClock{baseTime}, windowSize, grace, func(context.Context, model.WindowResult) error { return nil })

	invalids := []model.Event{
		{ID: "", EntityKey: "card_1", Value: 1, Timestamp: baseTime},
		{ID: "x", EntityKey: "", Value: 1, Timestamp: baseTime},
		{ID: "x", EntityKey: "card_1", Value: 1, Timestamp: time.Time{}},
	}
	for _, ev := range invalids {
		if err := agg.processEvent(ctx, 0, ev); err != nil {
			t.Fatalf("processEvent: %v", err)
		}
	}
	if got := agg.metrics.Invalid.Load(); got != 3 {
		t.Errorf("Invalid = %d, want 3", got)
	}
	if got := agg.metrics.Aggregated.Load(); got != 0 {
		t.Errorf("Aggregated = %d, want 0", got)
	}
}

// TestCloseWindows_EmitsOncePerWindow: N eventos numa janela produzem exatamente
// 1 WindowResult com Count = N, após windowEnd + grace.
func TestCloseWindows_EmitsOncePerWindow(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, t.TempDir())
	emit, out := captureEmitter()
	agg := newAggregator(store, fakeClock{baseTime}, windowSize, grace, emit)

	for _, ev := range makeEvents("card_1", 5) {
		if err := agg.processEvent(ctx, 0, ev); err != nil {
			t.Fatalf("processEvent: %v", err)
		}
	}
	// Janela ainda aberta: nada deve fechar.
	if err := agg.closeWindows(ctx); err != nil {
		t.Fatalf("closeWindows: %v", err)
	}
	if len(*out) != 0 {
		t.Fatalf("emitted %d before watermark advance, want 0", len(*out))
	}

	// Avança o watermark e fecha.
	if err := agg.processEvent(ctx, 0, triggerEvent()); err != nil {
		t.Fatalf("processEvent trigger: %v", err)
	}
	if err := agg.closeWindows(ctx); err != nil {
		t.Fatalf("closeWindows: %v", err)
	}

	if len(*out) != 1 {
		t.Fatalf("emitted %d windows, want 1", len(*out))
	}
	res := (*out)[0]
	if res.EntityKey != "card_1" {
		t.Errorf("EntityKey = %s, want card_1", res.EntityKey)
	}
	if res.Count != 5 {
		t.Errorf("Count = %d, want 5", res.Count)
	}
	if res.Sum != 10 { // 0+1+2+3+4
		t.Errorf("Sum = %v, want 10", res.Sum)
	}
	if res.Avg != 2 {
		t.Errorf("Avg = %v, want 2", res.Avg)
	}
	if !res.WindowStart.Equal(baseTime) {
		t.Errorf("WindowStart = %s, want %s", res.WindowStart, baseTime)
	}

	// Fechar de novo não reemite (estado já removido).
	if err := agg.closeWindows(ctx); err != nil {
		t.Fatalf("closeWindows again: %v", err)
	}
	if len(*out) != 1 {
		t.Errorf("re-emitted; total %d, want 1", len(*out))
	}
}

// TestConcurrent_ProcessAndClose exercita a corrida real entre o escritor
// (processEvent) e o leitor (closeWindows) em goroutines separadas. Deve passar
// sob -race graças ao copy-on-write dos escalares do WindowState.
func TestConcurrent_ProcessAndClose(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, t.TempDir())
	emit, _ := captureEmitter() // emit só é chamado pela goroutine de fechamento
	agg := newAggregator(store, fakeClock{baseTime}, windowSize, grace, emit)

	const n = 2000
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i := 0; i < n; i++ {
			ev := model.Event{
				ID:        fmt.Sprintf("e%d", i),
				EntityKey: fmt.Sprintf("card_%d", i%5),
				Value:     float64(i),
				Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			}
			_ = agg.processEvent(ctx, 0, ev)
		}
	}()

	closerDone := make(chan struct{})
	go func() {
		defer close(closerDone)
		for {
			select {
			case <-writerDone:
				return
			default:
				_ = agg.closeWindows(ctx)
			}
		}
	}()

	<-writerDone
	<-closerDone
	if err := agg.closeWindows(ctx); err != nil {
		t.Fatalf("final closeWindows: %v", err)
	}
}

// TestRecovery_IdenticalToNoCrash: matar o processo no meio da janela e reiniciar
// produz um WindowResult idêntico ao cenário sem crash.
func TestRecovery_IdenticalToNoCrash(t *testing.T) {
	ctx := context.Background()
	events := makeEvents("card_1", 10)

	// --- Run A: sem crash ---
	emitA, outA := captureEmitter()
	aggA := newAggregator(newTestStore(t, t.TempDir()), fakeClock{baseTime}, windowSize, grace, emitA)
	for _, ev := range events {
		if err := aggA.processEvent(ctx, 0, ev); err != nil {
			t.Fatalf("A processEvent: %v", err)
		}
	}
	if err := aggA.processEvent(ctx, 0, triggerEvent()); err != nil {
		t.Fatalf("A trigger: %v", err)
	}
	if err := aggA.closeWindows(ctx); err != nil {
		t.Fatalf("A closeWindows: %v", err)
	}
	if len(*outA) != 1 {
		t.Fatalf("A emitted %d, want 1", len(*outA))
	}
	resA := (*outA)[0]

	// --- Run B: crash no meio + recovery ---
	dirB := t.TempDir()

	// Instância 1: processa metade e faz snapshot, depois "morre".
	storeB1 := newTestStore(t, dirB)
	aggB1 := newAggregator(storeB1, fakeClock{baseTime}, windowSize, grace, func(context.Context, model.WindowResult) error { return nil })
	for _, ev := range events[:5] {
		if err := aggB1.processEvent(ctx, 0, ev); err != nil {
			t.Fatalf("B1 processEvent: %v", err)
		}
	}
	if err := storeB1.Snapshot(map[int]int64{0: 5}); err != nil {
		t.Fatalf("B1 Snapshot: %v", err)
	}

	// Instância 2: novo store no mesmo diretório, restaura e continua.
	storeB2 := newTestStore(t, dirB)
	restored, err := storeB2.Restore()
	if err != nil {
		t.Fatalf("B2 Restore: %v", err)
	}
	if restored[0] != 5 {
		t.Errorf("restored offset[0] = %d, want 5", restored[0])
	}

	emitB, outB := captureEmitter()
	aggB2 := newAggregator(storeB2, fakeClock{baseTime}, windowSize, grace, emitB)
	// Replay at-least-once: reprocessa TODA a janela (primeira metade deve ser
	// deduplicada pelo estado restaurado; segunda metade é nova).
	for _, ev := range events {
		if err := aggB2.processEvent(ctx, 0, ev); err != nil {
			t.Fatalf("B2 processEvent: %v", err)
		}
	}
	if err := aggB2.processEvent(ctx, 0, triggerEvent()); err != nil {
		t.Fatalf("B2 trigger: %v", err)
	}
	if err := aggB2.closeWindows(ctx); err != nil {
		t.Fatalf("B2 closeWindows: %v", err)
	}
	if len(*outB) != 1 {
		t.Fatalf("B emitted %d, want 1", len(*outB))
	}
	resB := (*outB)[0]

	// Resultado idêntico ao run sem crash.
	if resA.Count != resB.Count {
		t.Errorf("Count mismatch: A=%d B=%d", resA.Count, resB.Count)
	}
	if resA.Sum != resB.Sum {
		t.Errorf("Sum mismatch: A=%v B=%v", resA.Sum, resB.Sum)
	}
	if resA.Avg != resB.Avg {
		t.Errorf("Avg mismatch: A=%v B=%v", resA.Avg, resB.Avg)
	}
	if resB.Count != 10 {
		t.Errorf("recovered Count = %d, want 10", resB.Count)
	}
	// A primeira metade replayada deve ter sido deduplicada.
	if got := aggB2.metrics.Duplicates.Load(); got != 5 {
		t.Errorf("recovered Duplicates = %d, want 5", got)
	}
}
