package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// HostFacts — то, что служба знает о машине, не открывая снимка.
type HostFacts struct {
	ID       uuid.UUID
	Hostname string

	FirstSeenAt  time.Time
	LastReportAt time.Time

	// LastCollectedAt — время последнего сбора, а не доставки. Отчёт с
	// машины, которая простояла неделю без сети, приходит сегодня, но
	// рассказывает о прошлой неделе.
	LastCollectedAt *time.Time

	// LastChangeAt — когда состав машины менялся в последний раз. Пусто —
	// значит с первого отчёта ничего не менялось.
	LastChangeAt *time.Time

	SilentSince *time.Time
	RetiredAt   *time.Time
}

// Facts отдаёт сводку по одной машине.
//
// Нужна ровно затем, чтобы отличить «машина не отвечает» от «машина отвечает,
// но у неё ничего не изменилось». В ленту состояние уходит только при
// расхождении, и потребитель принципиально не может отличить одно от другого
// сам — а человеку, который только что нажал «обновить сейчас», разница
// важнее всего остального.
func (p *Pool) Facts(ctx context.Context, hostID uuid.UUID) (HostFacts, error) {
	var h HostFacts
	h.ID = hostID

	err := p.QueryRow(ctx, `
		SELECT hostname, first_seen_at, last_report_at, silent_since, retired_at,
		       (SELECT max(collected_at) FROM snapshots s WHERE s.host_id = h.id),
		       (SELECT max(detected_at)  FROM changes   c WHERE c.host_id = h.id)
		  FROM hosts h
		 WHERE id = $1`, hostID,
	).Scan(
		&h.Hostname, &h.FirstSeenAt, &h.LastReportAt, &h.SilentSince, &h.RetiredAt,
		&h.LastCollectedAt, &h.LastChangeAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return HostFacts{}, ErrNotFound
	}
	if err != nil {
		return HostFacts{}, fmt.Errorf("сводка по машине: %w", err)
	}
	return h, nil
}
