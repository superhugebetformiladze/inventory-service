package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// NetworkTarget — адрес для опроса.
type NetworkTarget struct {
	IP   string
	Name string
}

// NetworkTargetRow — адрес опроса со статусом последней попытки.
type NetworkTargetRow struct {
	IP            string
	Name          string
	Source        string
	AddedAt       time.Time
	LastAttemptAt *time.Time
	LastOKAt      *time.Time
	LastError     string
	DeviceID      string
	// SilentSince — из network_devices: устройство ответило хоть раз и
	// с тех пор молчит дольше порога.
	SilentSince *time.Time
}

// TargetsChange — итог замены списка источника.
type TargetsChange struct {
	Added, Removed, Total int
}

// ReplaceNetworkTargets заменяет строки источника списком целиком.
//
// Целиком, а не по одному: внешняя система присылает «вот все мои
// коммутаторы», и удалённый у неё коммутатор должен перестать опрашиваться
// без отдельной команды. Строки других источников не трогаются. Адрес,
// который уже положил другой источник, переходит к этому — опрашивать его
// дважды незачем, а кому он принадлежит, значит лишь, кто его уберёт.
func (p *Pool) ReplaceNetworkTargets(
	ctx context.Context, source string, targets []NetworkTarget,
) (TargetsChange, error) {
	var out TargetsChange

	ips := make([]string, 0, len(targets))
	names := make([]string, 0, len(targets))
	seen := make(map[string]bool, len(targets))
	for _, t := range targets {
		if seen[t.IP] {
			continue
		}
		seen[t.IP] = true
		ips = append(ips, t.IP)
		names = append(names, t.Name)
	}

	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM network_targets
			 WHERE source = $1 AND NOT (ip = ANY($2))`, source, ips)
		if err != nil {
			return fmt.Errorf("удаление адресов опроса: %w", err)
		}
		out.Removed = int(tag.RowsAffected())

		// xmax = 0 — строка вставлена, а не обновлена: так считается, сколько
		// адресов действительно новых.
		rows, err := tx.Query(ctx, `
			INSERT INTO network_targets (ip, name, source)
			SELECT ip, name, $3::text FROM unnest($1::text[], $2::text[]) AS t(ip, name)
			ON CONFLICT (ip) DO UPDATE
			   SET name = EXCLUDED.name, source = EXCLUDED.source
			RETURNING (xmax = 0)`, ips, names, source)
		if err != nil {
			return fmt.Errorf("запись адресов опроса: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var inserted bool
			if err := rows.Scan(&inserted); err != nil {
				return err
			}
			if inserted {
				out.Added++
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}

		return tx.QueryRow(ctx, `SELECT count(*) FROM network_targets`).Scan(&out.Total)
	})
	if err != nil {
		return TargetsChange{}, err
	}
	return out, nil
}

// ListNetworkTargets — все адреса опроса со статусом.
func (p *Pool) ListNetworkTargets(ctx context.Context) ([]NetworkTargetRow, error) {
	rows, err := p.Query(ctx, `
		SELECT t.ip, t.name, t.source, t.added_at, t.last_attempt_at, t.last_ok_at,
		       t.last_error, t.device_id, d.silent_since
		  FROM network_targets t
		  LEFT JOIN network_devices d ON d.device_id = t.device_id AND t.device_id <> ''
		 ORDER BY t.ip`)
	if err != nil {
		return nil, fmt.Errorf("список адресов опроса: %w", err)
	}
	defer rows.Close()

	var out []NetworkTargetRow
	for rows.Next() {
		var r NetworkTargetRow
		if err := rows.Scan(&r.IP, &r.Name, &r.Source, &r.AddedAt, &r.LastAttemptAt,
			&r.LastOKAt, &r.LastError, &r.DeviceID, &r.SilentSince); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordNetworkAttempt записывает итог попытки опроса адреса.
//
// errText пуст — устройство ответило. Адреса, которого нет в списке (его
// убрали, пока шёл проход), запись не касается.
func (p *Pool) RecordNetworkAttempt(
	ctx context.Context, ip, deviceID, errText string, now time.Time,
) error {
	if len(errText) > 500 {
		errText = errText[:500]
	}
	var err error
	if errText == "" {
		_, err = p.Exec(ctx, `
			UPDATE network_targets
			   SET last_attempt_at = $2, last_ok_at = $2, last_error = '', device_id = $3
			 WHERE ip = $1`, ip, now, deviceID)
	} else {
		_, err = p.Exec(ctx, `
			UPDATE network_targets
			   SET last_attempt_at = $2, last_error = $3
			 WHERE ip = $1`, ip, now, errText)
	}
	if err != nil {
		return fmt.Errorf("итог опроса %s: %w", ip, err)
	}
	return nil
}
