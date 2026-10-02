package storage

import (
	"context"
	"fmt"
	"time"
)

// Cleaned — что и сколько удалено за один проход.
type Cleaned struct {
	Snapshots  int64
	Outbox     int64
	Commands   int64
	Unresolved int64
}

// Cleanup удаляет то, что перестало быть нужным.
//
// Служба копит данные быстрее, чем кажется: пятьсот машин по шесть отчётов в
// сутки — это девять миллионов снимков в год, каждый по несколько десятков
// килобайт. Без чистки база растёт до размера, при котором её перестают
// бэкапить, — и это обнаруживается в тот день, когда её надо восстановить.
//
// История изменений (`changes`) НЕ чистится. Она и есть ответ на вопрос «кто
// вынул диск и когда», ради которого всё затевалось, и весит на три порядка
// меньше снимков.
func (p *Pool) Cleanup(ctx context.Context, snapshots, outbox, commands time.Duration) (Cleaned, error) {
	var c Cleaned

	// Последний снимок каждой машины остаётся всегда, каким бы старым он ни
	// был. Иначе машина, замолчавшая на два месяца, теряет в учёте всё, что о
	// ней известно, — и вместо «сведения устарели» получается «сведений нет».
	tag, err := p.Exec(ctx, `
		DELETE FROM snapshots s
		 WHERE s.received_at < now() - $1::interval
		   AND s.id <> (SELECT id FROM snapshots
		                 WHERE host_id = s.host_id
		                 ORDER BY collected_at DESC
		                 LIMIT 1)`, snapshots)
	if err != nil {
		return c, fmt.Errorf("чистка снимков: %w", err)
	}
	c.Snapshots = tag.RowsAffected()

	// Опубликованные сообщения: очередь, а не журнал. Их назначение
	// исчерпано в момент публикации.
	tag, err = p.Exec(ctx, `
		DELETE FROM outbox
		 WHERE published_at IS NOT NULL AND published_at < now() - $1::interval`, outbox)
	if err != nil {
		return c, fmt.Errorf("чистка очереди публикации: %w", err)
	}
	c.Outbox = tag.RowsAffected()

	// Выполненные и просроченные задания.
	tag, err = p.Exec(ctx, `
		DELETE FROM commands
		 WHERE (done_at IS NOT NULL AND done_at < now() - $1::interval)
		    OR (done_at IS NULL AND expires_at < now() - $1::interval)`, commands)
	if err != nil {
		return c, fmt.Errorf("чистка заданий: %w", err)
	}
	c.Commands = tag.RowsAffected()

	// Разобранные отчёты очереди: решение принято, снимок в них устарел.
	// Неразобранные не трогаются никогда — это чей-то невыполненный долг.
	tag, err = p.Exec(ctx, `
		DELETE FROM unresolved_reports
		 WHERE resolved_at IS NOT NULL AND resolved_at < now() - $1::interval`, snapshots)
	if err != nil {
		return c, fmt.Errorf("чистка очереди разбора: %w", err)
	}
	c.Unresolved = tag.RowsAffected()

	return c, nil
}
