package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrHostNotFound — машины с таким идентификатором в службе нет.
var ErrHostNotFound = errors.New("машина не найдена")

// Retired — что стало с машиной при выводе из учёта.
type Retired struct {
	HostID   uuid.UUID
	Hostname string

	// Known — машина была в базе службы. Ложь означает надгробие по
	// идентификатору из ленты: сама запись до этого уже исчезла.
	Known bool
}

// RetireHost выводит машину из учёта: ставит отметку и кладёт в outbox
// сообщение о выбытии.
//
// Отметка и сообщение — одной транзакцией (инвариант I2). Разорви их, и
// получится ровно то, из-за чего этот приём появился: в службе машины нет, а
// у всех потребителей она есть, и никакой отчёт больше не придёт, чтобы это
// исправить.
//
// Машина, которой в базе уже нет, тоже выводится. Это не послабление ради
// удобства: уплотняемый топик хранит запись по ключу навсегда, и
// единственный способ убрать оттуда машину, удалённую в обход службы, —
// отправить надгробие по её ключу. Без этого потребитель, поднятый с нуля,
// каждый раз воскрешал бы всё, что когда-либо удаляли руками.
func (p *Pool) RetireHost(
	ctx context.Context,
	hostID uuid.UUID,
	now time.Time,
	build func(Retired) (OutboxMessage, error),
) (Retired, error) {
	var out Retired
	out.HostID = hostID

	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			UPDATE hosts
			   SET retired_at = COALESCE(retired_at, $2)
			 WHERE id = $1
			 RETURNING hostname`, hostID, now).Scan(&out.Hostname)
		switch {
		case err == nil:
			out.Known = true
		case errors.Is(err, pgx.ErrNoRows):
			out.Known = false
		default:
			return fmt.Errorf("отметка о выбытии: %w", err)
		}

		msg, err := build(out)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO outbox (topic, key, payload) VALUES ($1, $2, $3)`,
			msg.Topic, msg.Key, msg.Payload); err != nil {
			return fmt.Errorf("запись в outbox: %w", err)
		}
		return nil
	})
	if err != nil {
		return Retired{}, err
	}
	return out, nil
}

// RetiredHost — строка о выбывшей машине для отчёта оператору.
type RetiredHost struct {
	ID        uuid.UUID
	Hostname  string
	RetiredAt time.Time
}

// ListRetired перечисляет выбывшие машины.
func (p *Pool) ListRetired(ctx context.Context, limit int) ([]RetiredHost, error) {
	rows, err := p.Query(ctx, `
		SELECT id, hostname, retired_at
		  FROM hosts
		 WHERE retired_at IS NOT NULL
		 ORDER BY retired_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("список выбывших: %w", err)
	}
	defer rows.Close()

	var out []RetiredHost
	for rows.Next() {
		var h RetiredHost
		if err := rows.Scan(&h.ID, &h.Hostname, &h.RetiredAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
