package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/broton07/inventory_service/internal/diff"
	"github.com/broton07/inventory_service/pkg/contract"
)

// detectChanges сравнивает новый снимок с последним принятым и записывает
// расхождения.
//
// Быстрый путь — сравнение отпечатков. Он и есть главный: девятнадцать
// отчётов из двадцати ничем не отличаются от предыдущего, и на них не должно
// уходить ни одного лишнего запроса.
func detectChanges(
	ctx context.Context, tx pgx.Tx,
	hostID, snapshotID uuid.UUID, snap contract.Snapshot, firstSeen bool, now time.Time,
) ([]contract.Change, error) {
	fingerprint := diff.Fingerprint(snap)

	var previous []byte
	if !firstSeen {
		if err := tx.QueryRow(ctx,
			`SELECT fingerprint FROM hosts WHERE id = $1`, hostID).Scan(&previous); err != nil {
			return nil, fmt.Errorf("отпечаток машины: %w", err)
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE hosts SET fingerprint = $2 WHERE id = $1`, hostID, fingerprint); err != nil {
		return nil, fmt.Errorf("сохранение отпечатка: %w", err)
	}

	switch {
	case firstSeen:
		// Первый снимок сравнивать не с чем. «Появилась память 8 ГБ» на
		// новой машине — это не изменение, а её описание.
		return nil, nil
	case previous == nil:
		// Машина известна, но отпечатка нет: снимок пришёл до этого этапа.
		// Первый отчёт после обновления службы дал бы «изменилось всё» по
		// всему парку — поэтому молча запоминаем и ждём следующего.
		return nil, nil
	case string(previous) == string(fingerprint):
		return nil, nil
	}

	was, err := previousSnapshot(ctx, tx, hostID, snapshotID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	changes := diff.Compare(was, snap)
	if len(changes) == 0 {
		// Отпечаток разошёлся, а значимых расхождений нет. Так бывает, когда
		// изменилось поле, которое входит в отпечаток, но не описано в
		// сравнении по-человечески. Событие в этом случае не публикуется
		// (инвариант I9).
		return nil, nil
	}

	for _, c := range changes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO changes (host_id, snapshot_id, detected_at, field, old_value, new_value)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			hostID, snapshotID, now, c.Field, c.Old, c.New); err != nil {
			return nil, fmt.Errorf("запись изменения: %w", err)
		}
	}

	return changes, nil
}

// previousSnapshot — последний снимок машины, кроме только что вставленного.
func previousSnapshot(ctx context.Context, tx pgx.Tx, hostID, exclude uuid.UUID) (contract.Snapshot, error) {
	var payload []byte

	err := tx.QueryRow(ctx, `
		SELECT payload FROM snapshots
		 WHERE host_id = $1 AND id <> $2
		 ORDER BY collected_at DESC
		 LIMIT 1`, hostID, exclude).Scan(&payload)

	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Snapshot{}, ErrNotFound
	}
	if err != nil {
		return contract.Snapshot{}, fmt.Errorf("предыдущий снимок: %w", err)
	}

	var snap contract.Snapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return contract.Snapshot{}, fmt.Errorf("разбор предыдущего снимка: %w", err)
	}
	return snap, nil
}

// HostChange — строка истории железа.
type HostChange struct {
	DetectedAt time.Time
	Field      string
	Old        string
	New        string
}

// History — история изменений машины, свежие сверху.
func (p *Pool) History(ctx context.Context, hostID uuid.UUID, limit int) ([]HostChange, error) {
	rows, err := p.Query(ctx, `
		SELECT detected_at, field, old_value, new_value
		  FROM changes
		 WHERE host_id = $1
		 ORDER BY detected_at DESC, id DESC
		 LIMIT $2`, hostID, limit)
	if err != nil {
		return nil, fmt.Errorf("история машины: %w", err)
	}
	defer rows.Close()

	var out []HostChange
	for rows.Next() {
		var c HostChange
		if err := rows.Scan(&c.DetectedAt, &c.Field, &c.Old, &c.New); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SilentHost — машина, переставшая выходить на связь.
type SilentHost struct {
	ID         uuid.UUID
	Hostname   string
	LastReport time.Time

	// FirstSeen и Snapshot нужны, чтобы вместе с событием о молчании
	// обновить запись машины в уплотняемом топике.
	//
	// Снимок обязателен, а не «хорошо бы»: уплотняемый топик хранит по
	// ключу ТОЛЬКО последнюю запись. Отправь туда состояние без снимка — и
	// потребитель, поднятый с нуля через год, узнает о машине лишь то, что
	// она молчит, а чем она была, не узнает уже никогда.
	FirstSeen time.Time
	Snapshot  contract.Snapshot
}

// MarkSilent помечает молчащие машины и складывает сообщения о них в outbox.
//
// Пометка и сообщение — одной транзакцией (инвариант I2), и пометка нужна не
// для отчётности: без неё событие о молчании уходило бы на каждом проходе
// задачи, пока машина стоит выключенная в отпуске владельца.
func (p *Pool) MarkSilent(
	ctx context.Context,
	silentBefore, now time.Time,
	limit int,
	build func(SilentHost) ([]OutboxMessage, error),
) ([]SilentHost, error) {
	var found []SilentHost

	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT h.id, h.hostname, h.last_report_at, h.first_seen_at,
			       (SELECT s.payload FROM snapshots s
			         WHERE s.host_id = h.id
			         ORDER BY s.collected_at DESC
			         LIMIT 1)
			  FROM hosts h
			 WHERE h.silent_since IS NULL AND h.retired_at IS NULL AND h.last_report_at < $1
			 ORDER BY h.last_report_at
			 LIMIT $2
			 FOR UPDATE OF h SKIP LOCKED`, silentBefore, limit)
		if err != nil {
			return fmt.Errorf("поиск молчащих машин: %w", err)
		}

		for rows.Next() {
			var (
				h       SilentHost
				payload []byte
			)
			if err := rows.Scan(&h.ID, &h.Hostname, &h.LastReport, &h.FirstSeen, &payload); err != nil {
				rows.Close()
				return err
			}
			if len(payload) > 0 {
				// Нечитаемый снимок не повод бросить всю работу: событие о
				// молчании важнее, чем полнота записи в уплотняемом топике.
				_ = json.Unmarshal(payload, &h.Snapshot)
			}
			found = append(found, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(found) == 0 {
			return nil
		}

		ids := make([]uuid.UUID, len(found))
		for i, h := range found {
			ids[i] = h.ID

			msgs, err := build(h)
			if err != nil {
				return err
			}
			for _, msg := range msgs {
				if _, err := tx.Exec(ctx,
					`INSERT INTO outbox (topic, key, payload) VALUES ($1, $2, $3)`,
					msg.Topic, msg.Key, msg.Payload); err != nil {
					return fmt.Errorf("запись в outbox: %w", err)
				}
			}
		}

		if _, err := tx.Exec(ctx,
			`UPDATE hosts SET silent_since = $2 WHERE id = ANY($1)`, ids, now); err != nil {
			return fmt.Errorf("пометка молчащих: %w", err)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return found, nil
}
