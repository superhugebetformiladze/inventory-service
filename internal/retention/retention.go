// Package retention — чистка того, что перестало быть нужным.
//
// Отдельной задачей внутри службы, а не заданием cron на сервере: cron живёт
// в чужой ответственности, его забывают перенести при переезде, и обнаружить
// это можно только по размеру базы через полгода. Служба, которая чистит за
// собой сама, переезжает целиком.
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/broton07/inventory_service/internal/storage"
)

// Сроки хранения по умолчанию.
//
// Снимки — месяц: за него успевает пройти любой рабочий цикл, а вопросы
// глубже месяца задают истории изменений, а не снимкам. Очередь публикации —
// неделя: это очередь, а не журнал, и держать её дольше незачем, но неделя
// оставляет запас на разбор происшествия. Задания — месяц: по ним видно, кто
// просил внеочередной сбор и чем это кончилось.
const (
	DefaultSnapshots = 30 * 24 * time.Hour
	DefaultOutbox    = 7 * 24 * time.Hour
	DefaultCommands  = 30 * 24 * time.Hour

	// DefaultInterval — как часто чистить.
	//
	// Раз в сутки: чистка удаляет миллионы строк и нагружает базу, а разница
	// между «данные старше месяца» и «старше месяца и часа» никого не
	// интересует.
	DefaultInterval = 24 * time.Hour
)

// Store — то, что чистке нужно от базы.
type Store interface {
	Cleanup(ctx context.Context, snapshots, outbox, commands time.Duration) (storage.Cleaned, error)
}

// Options — сроки хранения.
type Options struct {
	Snapshots time.Duration
	Outbox    time.Duration
	Commands  time.Duration
	Interval  time.Duration
	Logger    *slog.Logger
}

// Cleaner чистит базу по расписанию.
type Cleaner struct {
	store Store
	opts  Options
}

// New создаёт уборщика.
func New(store Store, opts Options) *Cleaner {
	if opts.Snapshots <= 0 {
		opts.Snapshots = DefaultSnapshots
	}
	if opts.Outbox <= 0 {
		opts.Outbox = DefaultOutbox
	}
	if opts.Commands <= 0 {
		opts.Commands = DefaultCommands
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Cleaner{store: store, opts: opts}
}

// Run чистит, пока не отменят контекст.
//
// Первый проход — не сразу при старте. Перезапуск службы под нагрузкой не
// должен добавлять к ней ещё и удаление миллиона строк; а если служба
// перезапускается часто, чистка при каждом старте превращается в постоянную
// нагрузку.
func (c *Cleaner) Run(ctx context.Context) {
	ticker := time.NewTicker(c.opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Tick(ctx)
		}
	}
}

// Tick — один проход. Вынесен отдельно ради тестов и разового запуска руками.
func (c *Cleaner) Tick(ctx context.Context) storage.Cleaned {
	// Свой срок: чистка миллиона строк не должна висеть вечно, но и десяти
	// секунд ей мало.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cleaned, err := c.store.Cleanup(ctx, c.opts.Snapshots, c.opts.Outbox, c.opts.Commands)
	if err != nil {
		c.opts.Logger.Error("чистка не удалась", "err", err)
		return storage.Cleaned{}
	}

	// Пишется только когда что-то удалено: ежедневная запись «удалено ноль»
	// не сообщает ничего и приучает пролистывать журнал.
	if cleaned.Snapshots+cleaned.Outbox+cleaned.Commands+cleaned.Unresolved > 0 {
		c.opts.Logger.Info("чистка выполнена",
			"snapshots", cleaned.Snapshots,
			"outbox", cleaned.Outbox,
			"commands", cleaned.Commands,
			"unresolved", cleaned.Unresolved)
	}

	return cleaned
}
