// Package outbox — релей: переносит сообщения из таблицы в ленту.
//
// Отдельный от приёма процесс существует ровно затем, чтобы приём не зависел
// от доступности ленты. Агент, которому служба не ответила, потому что Kafka
// на перезапуске, повторит отправку — и так пятьсот агентов сразу.
package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/broton07/inventory_service/internal/metrics"
	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/storage"
)

// Store — то, что релею нужно от базы.
type Store interface {
	ProcessPending(ctx context.Context, limit int, publish func(context.Context, []storage.PendingMessage) error) (int, error)
	NoteFailure(ctx context.Context, reason string, limit int) error
}

// Relay периодически переносит накопившееся в ленту.
type Relay struct {
	store     Store
	publisher publish.Publisher
	log       *slog.Logger

	interval  time.Duration
	batchSize int

	// backoff растёт, пока лента недоступна. Без него упавшая Kafka означала
	// бы запрос к базе и попытку подключения каждую секунду от каждого
	// экземпляра службы — то есть добивание того, что и так лежит.
	backoff    time.Duration
	maxBackoff time.Duration
}

// Options — настройки релея.
type Options struct {
	Interval   time.Duration
	BatchSize  int
	MaxBackoff time.Duration
	Logger     *slog.Logger
}

// New создаёт релей.
func New(store Store, publisher publish.Publisher, opts Options) *Relay {
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.BatchSize <= 0 {
		// Пачка небольшая намеренно: отправка идёт внутри транзакции базы, и
		// длинная пачка держит блокировки строк дольше, чем нужно.
		opts.BatchSize = 100
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Relay{
		store:      store,
		publisher:  publisher,
		log:        opts.Logger.With("component", "outbox-relay"),
		interval:   opts.Interval,
		batchSize:  opts.BatchSize,
		maxBackoff: opts.MaxBackoff,
	}
}

// Run крутит релей до отмены контекста.
func (r *Relay) Run(ctx context.Context) {
	r.log.Info("релей запущен", "interval", r.interval, "batch", r.batchSize)

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			r.log.Info("релей остановлен")
			return
		case <-timer.C:
		}

		wait := r.interval
		if n, err := r.Tick(ctx); err != nil {
			wait = r.nextBackoff()
			r.log.Error("проход релея не удался", "err", err, "повтор через", wait)
		} else {
			r.backoff = 0
			if n > 0 {
				r.log.Debug("отправлено сообщений", "count", n)
				// Очередь могла быть длиннее пачки — не ждём такта.
				wait = 0
			}
		}

		timer.Reset(wait)
	}
}

// Tick — один проход. Вынесен отдельно, чтобы его можно было вызвать из теста
// и из ручной команды, не заводя цикл.
func (r *Relay) Tick(ctx context.Context) (int, error) {
	n, err := r.store.ProcessPending(ctx, r.batchSize,
		func(ctx context.Context, msgs []storage.PendingMessage) error {
			out := make([]publish.Message, len(msgs))
			for i, m := range msgs {
				out[i] = publish.Message{Topic: m.Topic, Key: m.Key, Value: m.Payload}
			}

			if err := r.publisher.Publish(ctx, out); err != nil {
				metrics.PublishFailuresTotal.Inc()
				return err
			}

			// Счётчик растёт после успешной публикации, а не до: иначе на
			// графике опубликовано больше, чем дошло до ленты, и разобраться
			// в расхождении потом будет нечем.
			for _, m := range msgs {
				metrics.PublishedTotal.WithLabelValues(m.Topic).Inc()
			}
			return nil
		})

	if err != nil {
		// Отметка о неудаче — отдельной транзакцией: та, что внутри
		// ProcessPending, уже откатилась вместе с ошибкой.
		if noteErr := r.store.NoteFailure(context.WithoutCancel(ctx), err.Error(), r.batchSize); noteErr != nil {
			r.log.Warn("не удалось записать причину неудачи", "err", noteErr)
		}
		return 0, err
	}

	return n, nil
}

func (r *Relay) nextBackoff() time.Duration {
	if r.backoff == 0 {
		r.backoff = r.interval
	} else {
		r.backoff *= 2
	}
	if r.backoff > r.maxBackoff {
		r.backoff = r.maxBackoff
	}
	return r.backoff
}
