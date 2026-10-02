// Package silence замечает машины, переставшие выходить на связь.
//
// Событие здесь порождает не отчёт, а его отсутствие — поэтому источником
// служит периодическая задача, а не приём. Это единственное место в службе,
// где что-то происходит без входящего запроса.
package silence

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// Store — то, что нужно от базы.
type Store interface {
	MarkSilent(ctx context.Context, silentBefore, now time.Time, limit int,
		build func(storage.SilentHost) ([]storage.OutboxMessage, error)) ([]storage.SilentHost, error)
}

// Watcher периодически проверяет, кто замолчал.
type Watcher struct {
	store Store
	log   *slog.Logger

	// Threshold — сколько машина может молчать, прежде чем это станет
	// событием.
	//
	// Порог должен быть заметно больше периода сбора: машина, выключенная на
	// ночь и включённая утром, не должна попадать в список каждое утро.
	threshold time.Duration

	interval time.Duration
	batch    int
	now      func() time.Time
}

// Options — настройки наблюдателя.
type Options struct {
	Threshold time.Duration
	Interval  time.Duration
	Batch     int
	Logger    *slog.Logger
	Now       func() time.Time
}

// New создаёт наблюдателя.
func New(store Store, opts Options) *Watcher {
	if opts.Threshold <= 0 {
		opts.Threshold = 7 * 24 * time.Hour
	}
	if opts.Interval <= 0 {
		// Проверять чаще незачем: порог измеряется днями, и проход по
		// частичному индексу стоит дёшево только потому, что редок.
		opts.Interval = time.Hour
	}
	if opts.Batch <= 0 {
		opts.Batch = 500
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	return &Watcher{
		store:     store,
		log:       opts.Logger.With("component", "silence"),
		threshold: opts.Threshold,
		interval:  opts.Interval,
		batch:     opts.Batch,
		now:       opts.Now,
	}
}

// Run крутит проверку до отмены контекста.
func (w *Watcher) Run(ctx context.Context) {
	w.log.Info("наблюдение за молчащими машинами запущено",
		"threshold", w.threshold, "interval", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		if n, err := w.Tick(ctx); err != nil {
			w.log.Error("проход не удался", "err", err)
		} else if n > 0 {
			w.log.Info("замечены молчащие машины", "count", n)
		}

		select {
		case <-ctx.Done():
			w.log.Info("наблюдение остановлено")
			return
		case <-ticker.C:
		}
	}
}

// Tick — один проход. Вынесен отдельно, чтобы его можно было вызвать из теста
// и из ручной команды, не заводя цикл.
func (w *Watcher) Tick(ctx context.Context) (int, error) {
	now := w.now().UTC()

	found, err := w.store.MarkSilent(ctx, now.Add(-w.threshold), now, w.batch,
		func(h storage.SilentHost) ([]storage.OutboxMessage, error) {
			return w.messages(h, now)
		})
	if err != nil {
		return 0, err
	}

	for _, h := range found {
		w.log.Warn("машина не выходит на связь",
			"host_id", h.ID, "hostname", h.Hostname, "last_report", h.LastReport)
	}

	return len(found), nil
}

// messages — событие о молчании и обновлённое состояние машины.
//
// Двумя сообщениями, а не одним: событие рассказывает тому, кто слушает
// ленту сейчас, а состояние отвечает тому, кто поднимется через месяц и
// прочитает уплотняемый топик с начала. Без второго машина, замолчавшая
// прошлой весной, для нового потребителя выглядит совершенно здоровой.
func (w *Watcher) messages(h storage.SilentHost, now time.Time) ([]storage.OutboxMessage, error) {
	event, err := w.message(h, now)
	if err != nil {
		return nil, err
	}

	silentSince := now
	state := contract.HostState{
		SchemaVersion: contract.SchemaVersion,
		HostID:        h.ID.String(),
		FirstSeenAt:   h.FirstSeen,
		LastReportAt:  h.LastReport,
		Snapshot:      h.Snapshot,
		SilentSince:   &silentSince,
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("сериализация состояния: %w", err)
	}

	return []storage.OutboxMessage{
		event,
		{Topic: publish.TopicHosts, Key: h.ID.String(), Payload: payload},
	}, nil
}

func (w *Watcher) message(h storage.SilentHost, now time.Time) (storage.OutboxMessage, error) {
	дней := int(now.Sub(h.LastReport).Hours() / 24)

	event := contract.Event{
		SchemaVersion: contract.SchemaVersion,
		Type:          contract.EventSilent,
		HostID:        h.ID.String(),
		OccurredAt:    now,
		Reason:        fmt.Sprintf("не выходит на связь %d дн., последний отчёт %s", дней, h.LastReport.Format("2006-01-02 15:04")),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return storage.OutboxMessage{}, fmt.Errorf("сериализация события: %w", err)
	}

	return storage.OutboxMessage{
		Topic:   publish.TopicEvents,
		Key:     h.ID.String(), // инвариант I4
		Payload: payload,
	}, nil
}
