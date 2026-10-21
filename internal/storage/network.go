package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// NetworkDeviceRecord — снимок устройства, готовый к записи.
type NetworkDeviceRecord struct {
	DeviceID     string
	ManagementIP string
	SysName      string
	CollectedAt  time.Time
	Payload      []byte
	Hash         string
}

// SavedNetworkDevice — итог записи.
type SavedNetworkDevice struct {
	DeviceID    string
	FirstSeen   bool
	FirstSeenAt time.Time
	// Changed — отпечаток отличается от прежнего (или устройство новое).
	Changed bool
	// Returned — устройство числилось молчащим и ответило. Повод
	// опубликовать состояние, даже если в нём ничего не изменилось: снять
	// пометку о молчании у потребителей может только новое сообщение.
	Returned bool
}

// SaveNetworkDevice записывает снимок устройства и сообщения ленты ОДНОЙ
// транзакцией (инвариант I2).
//
// Строка блокируется на время сравнения: два опроса одного устройства
// (периодический и ручной) иначе оба увидели бы старый отпечаток, и в ленту
// ушло бы два состояния в неопределённом порядке.
//
// Снимок из файла может оказаться старше записанного — его сняли на флешку
// неделю назад. Такой снимок не затирает свежий: записывается, только если
// collected_at не раньше уже лежащего.
func (p *Pool) SaveNetworkDevice(
	ctx context.Context,
	rec NetworkDeviceRecord,
	now time.Time,
	build func(SavedNetworkDevice) ([]OutboxMessage, error),
) (SavedNetworkDevice, error) {
	out := SavedNetworkDevice{DeviceID: rec.DeviceID}

	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		var (
			oldHash     string
			collectedAt time.Time
			silentSince *time.Time
		)
		err := tx.QueryRow(ctx, `
			SELECT hash, first_seen_at, collected_at, silent_since
			  FROM network_devices
			 WHERE device_id = $1
			 FOR UPDATE`, rec.DeviceID).Scan(&oldHash, &out.FirstSeenAt, &collectedAt, &silentSince)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
			out.FirstSeen, out.Changed, out.FirstSeenAt = true, true, now
			if _, err := tx.Exec(ctx, `
				INSERT INTO network_devices
				       (device_id, management_ip, sys_name, first_seen_at, last_polled_at,
				        changed_at, collected_at, payload, hash)
				VALUES ($1, $2, $3, $4, $4, $4, $5, $6, $7)`,
				rec.DeviceID, rec.ManagementIP, rec.SysName, now, rec.CollectedAt,
				rec.Payload, rec.Hash); err != nil {
				return fmt.Errorf("запись устройства: %w", err)
			}
		case err != nil:
			return fmt.Errorf("чтение устройства: %w", err)
		case rec.CollectedAt.Before(collectedAt):
			// Снимок старше записанного — ничего не меняем.
			return nil
		default:
			out.Changed = oldHash != rec.Hash
			out.Returned = silentSince != nil
			// Ответило — значит, не молчит: и счёт неудач, и пометка
			// о молчании снимаются.
			if _, err := tx.Exec(ctx, `
				UPDATE network_devices
				   SET management_ip  = $2,
				       sys_name       = $3,
				       last_polled_at = $4,
				       changed_at     = CASE WHEN $7 THEN $4 ELSE changed_at END,
				       collected_at   = $5,
				       payload        = $6,
				       hash           = $8,
				       failing_since  = NULL,
				       silent_since   = NULL,
				       last_error     = ''
				 WHERE device_id = $1`,
				rec.DeviceID, rec.ManagementIP, rec.SysName, now, rec.CollectedAt,
				rec.Payload, out.Changed, rec.Hash); err != nil {
				return fmt.Errorf("обновление устройства: %w", err)
			}
		}

		msgs, err := build(out)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO outbox (topic, key, payload) VALUES ($1, $2, $3)`,
				m.Topic, m.Key, m.Payload); err != nil {
				return fmt.Errorf("запись в outbox: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return SavedNetworkDevice{}, err
	}
	return out, nil
}

// NetworkDeviceRow — строка для списка оператору.
type NetworkDeviceRow struct {
	DeviceID     string
	ManagementIP string
	SysName      string
	LastPolledAt time.Time
	ChangedAt    time.Time
	Ports        int
}

// ListNetworkDevices — опрошенные устройства по адресу.
func (p *Pool) ListNetworkDevices(ctx context.Context) ([]NetworkDeviceRow, error) {
	rows, err := p.Query(ctx, `
		SELECT device_id, management_ip, sys_name, last_polled_at, changed_at,
		       COALESCE(jsonb_array_length(payload->'ports'), 0)
		  FROM network_devices
		 ORDER BY management_ip`)
	if err != nil {
		return nil, fmt.Errorf("список устройств: %w", err)
	}
	defer rows.Close()

	var out []NetworkDeviceRow
	for rows.Next() {
		var r NetworkDeviceRow
		if err := rows.Scan(&r.DeviceID, &r.ManagementIP, &r.SysName,
			&r.LastPolledAt, &r.ChangedAt, &r.Ports); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkNetworkFailure отмечает неудачный опрос по адресу управления.
//
// По адресу, а не по идентификатору: не ответившее устройство не сообщило
// свой MAC шасси. Начало серии неудач не сдвигается следующими неудачами —
// по нему считается, сколько устройство молчит. Устройство, ни разу не
// ответившее, в таблице не появляется: заводить его не из чего.
func (p *Pool) MarkNetworkFailure(ctx context.Context, ip, reason string, now time.Time) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err := p.Exec(ctx, `
		UPDATE network_devices
		   SET failing_since = COALESCE(failing_since, $2),
		       last_error    = $3
		 WHERE management_ip = $1`, ip, now, reason)
	if err != nil {
		return fmt.Errorf("отметка неудачного опроса: %w", err)
	}
	return nil
}

// SilentNetworkDevice — устройство, замолчавшее дольше порога.
type SilentNetworkDevice struct {
	DeviceID     string
	ManagementIP string
	FirstSeenAt  time.Time
	LastPolledAt time.Time
	SilentSince  time.Time
	LastError    string
	Payload      []byte
}

// MarkNetworkSilent находит устройства, не отвечающие с before и раньше, и
// публикует о каждом состояние с пометкой о молчании — одной транзакцией с
// самой пометкой (I2). Уже помеченные повторно не публикуются (I9).
func (p *Pool) MarkNetworkSilent(
	ctx context.Context,
	before time.Time,
	build func(SilentNetworkDevice) (OutboxMessage, error),
) ([]SilentNetworkDevice, error) {
	var found []SilentNetworkDevice

	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT device_id, management_ip, first_seen_at, last_polled_at,
			       failing_since, last_error, payload
			  FROM network_devices
			 WHERE failing_since IS NOT NULL
			   AND silent_since IS NULL
			   AND failing_since <= $1
			 FOR UPDATE`, before)
		if err != nil {
			return fmt.Errorf("поиск молчащих устройств: %w", err)
		}
		for rows.Next() {
			var d SilentNetworkDevice
			if err := rows.Scan(&d.DeviceID, &d.ManagementIP, &d.FirstSeenAt, &d.LastPolledAt,
				&d.SilentSince, &d.LastError, &d.Payload); err != nil {
				rows.Close()
				return err
			}
			found = append(found, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, d := range found {
			msg, err := build(d)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO outbox (topic, key, payload) VALUES ($1, $2, $3)`,
				msg.Topic, msg.Key, msg.Payload); err != nil {
				return fmt.Errorf("запись в outbox: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE network_devices SET silent_since = failing_since WHERE device_id = $1`,
				d.DeviceID); err != nil {
				return fmt.Errorf("пометка молчания: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}
