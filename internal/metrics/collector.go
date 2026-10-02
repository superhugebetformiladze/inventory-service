package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/broton07/inventory_service/internal/storage"
)

// DefaultInterval — как часто пересчитывать показатели.
//
// Пятнадцать секунд: чаще не нужно — их всё равно снимают раз в пятнадцать,
// а каждый пересчёт это запрос к базе. Реже — и всплеск в очереди публикации
// успеет начаться и закончиться между двумя замерами.
const DefaultInterval = 15 * time.Second

// Source — то, что коллектору нужно от базы.
type Source interface {
	Snapshot(ctx context.Context) (storage.Stats, error)
}

// Collector периодически обновляет показатели, которые нельзя посчитать
// приращением.
//
// Счётчики (принято отчётов, опубликовано сообщений) растут прямо в коде,
// который это делает. А «сколько сейчас в очереди» и «сколько машин молчит» —
// это состояние базы, и узнать его можно только спросив.
type Collector struct {
	Source   Source
	Interval time.Duration
	Logger   *slog.Logger
}

// Run обновляет показатели, пока не отменят контекст.
func (c *Collector) Run(ctx context.Context) {
	interval := c.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}

	// Первый замер сразу: иначе первые пятнадцать секунд после запуска
	// графики показывают нули, и это неотличимо от «служба не работает».
	c.Tick(ctx)

	ticker := time.NewTicker(interval)
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

// Tick — один замер. Вынесен отдельно ради тестов.
func (c *Collector) Tick(ctx context.Context) {
	// Свой срок у замера: показатели не должны висеть на неотвечающей базе
	// дольше, чем интервал между замерами (I11).
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stats, err := c.Source.Snapshot(ctx)
	if err != nil {
		// Не ошибка службы: база могла быть занята. Но молчать нельзя —
		// иначе графики замрут на старых значениях и будут выглядеть живыми.
		c.Logger.Warn("не удалось снять показатели", "err", err)
		return
	}

	OutboxPending.Set(float64(stats.OutboxPending))
	OutboxLagSeconds.Set(stats.OutboxLag.Seconds())

	HostsTotal.Set(float64(stats.Hosts))
	HostsSilent.Set(float64(stats.HostsSilent))
	OldestReportSeconds.Set(stats.OldestReport.Seconds())

	UnresolvedTotal.Set(float64(stats.Unresolved))
	CommandsPending.Set(float64(stats.Commands))
}
