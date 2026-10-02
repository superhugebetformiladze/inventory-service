package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PendingMessage — сообщение, ожидающее отправки в ленту.
type PendingMessage struct {
	ID      int64
	Topic   string
	Key     string
	Payload []byte
}

// ProcessPending забирает пачку неотправленных сообщений, отдаёт их publish и,
// если тот справился, помечает отправленными — всё внутри одной транзакции.
//
// Одной транзакцией потому, что блокировка FOR UPDATE живёт ровно столько,
// сколько транзакция. Разбей это на «выбрать» и «отметить» двумя запросами по
// пулу — блокировка снимется сразу после выборки, и второй релей возьмёт те же
// строки. Отправка при этом происходит внутри транзакции: пачки маленькие,
// а альтернатива — гонка за строки.
//
// Возвращает количество обработанных сообщений.
func (p *Pool) ProcessPending(
	ctx context.Context,
	limit int,
	publish func(ctx context.Context, msgs []PendingMessage) error,
) (int, error) {
	var processed int

	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		msgs, err := fetchPending(ctx, tx, limit)
		if err != nil {
			return err
		}
		if len(msgs) == 0 {
			return nil
		}

		if err := publish(ctx, msgs); err != nil {
			// Транзакция откатится, published_at не проставится, сообщения
			// останутся в очереди. Счётчик попыток пишем отдельно, вне этой
			// транзакции, — иначе он откатится вместе со всем остальным.
			return fmt.Errorf("отправка в ленту: %w", err)
		}

		ids := make([]int64, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}

		if _, err := tx.Exec(ctx,
			`UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
			// Отправка уже состоялась, а отметка — нет. Сообщения уйдут
			// повторно, и это осознанный перекос: потеря необратима, а дубль
			// обрабатывается идемпотентным потребителем (инвариант I3).
			return fmt.Errorf("отметка отправленных: %w", err)
		}

		processed = len(msgs)
		return nil
	})

	return processed, err
}

// fetchPending выбирает и блокирует пачку.
//
// SKIP LOCKED — чтобы два релея не отправили одно сообщение дважды.
// ORDER BY id — чтобы события одной машины ушли в ленту в том порядке, в
// котором произошли: потребитель, получивший «стало 16 ГБ» раньше, чем «стало
// 8 ГБ», запишет в карточку неправильное.
func fetchPending(ctx context.Context, tx pgx.Tx, limit int) ([]PendingMessage, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, topic, key, payload
		  FROM outbox
		 WHERE published_at IS NULL
		 ORDER BY id
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("выборка из outbox: %w", err)
	}
	defer rows.Close()

	var out []PendingMessage
	for rows.Next() {
		var m PendingMessage
		if err := rows.Scan(&m.ID, &m.Topic, &m.Key, &m.Payload); err != nil {
			return nil, fmt.Errorf("чтение строки outbox: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// NoteFailure запоминает, почему отправка не удалась.
//
// Отдельной транзакцией, вне ProcessPending: запись внутри откатившейся
// транзакции исчезла бы вместе с ней, и застрявшее сообщение выглядело бы как
// нетронутое. Счётчик нужен не чтобы бросить попытки, а чтобы затор было
// видно: растущий attempts у одной строки — это сломанная лента, а не
// медленная.
func (p *Pool) NoteFailure(ctx context.Context, reason string, limit int) error {
	_, err := p.Exec(ctx, `
		UPDATE outbox
		   SET attempts = attempts + 1,
		       last_error = $1
		 WHERE id IN (
		     SELECT id FROM outbox WHERE published_at IS NULL ORDER BY id LIMIT $2
		 )`, reason, limit)
	if err != nil {
		return fmt.Errorf("отметка неудачи: %w", err)
	}
	return nil
}

// OutboxStats — состояние очереди для метрик и проверок.
type OutboxStats struct {
	Pending int
	Oldest  *time.Time
}

// Stats возвращает состояние очереди.
//
// Возраст самого старого неотправленного сообщения — главный показатель
// здоровья: когда лента недоступна, растёт он, а не количество.
func (p *Pool) Stats(ctx context.Context) (OutboxStats, error) {
	var s OutboxStats
	err := p.QueryRow(ctx, `
		SELECT count(*), min(created_at)
		  FROM outbox
		 WHERE published_at IS NULL`).Scan(&s.Pending, &s.Oldest)
	if err != nil {
		return OutboxStats{}, fmt.Errorf("состояние outbox: %w", err)
	}
	return s, nil
}
