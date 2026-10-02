package storage

import (
	"context"
	"fmt"
	"time"
)

// Stats — то, что служба знает о себе одним числом каждое.
type Stats struct {
	OutboxPending int64
	OutboxLag     time.Duration

	Hosts       int64
	HostsSilent int64

	// OldestReport — сколько прошло с отчёта самой давно отчитывавшейся
	// живой машины. Показатель парка: растёт, когда машины перестают
	// отчитываться поодиночке.
	OldestReport time.Duration

	// LastReport — сколько прошло с последнего отчёта вообще, от кого
	// угодно. Показатель приёма, а не парка, и путать их нельзя: пока хоть
	// одна машина отчитывается, приём работает, сколько бы ни молчали
	// остальные.
	LastReport time.Duration

	Unresolved int64
	Commands   int64
}

// Snapshot собирает показатели службы.
//
// Одним запросом, а не семью: показатели читаются раз в несколько секунд
// фоновой задачей, и семь отдельных обращений к базе ради этого — семь
// лишних соединений в момент, когда база и так под нагрузкой. Подзапросы
// считаются по индексам и стоят дёшево.
func (p *Pool) Snapshot(ctx context.Context) (Stats, error) {
	var (
		s          Stats
		lagSeconds float64
		oldestSecs float64
		lastSecs   float64
	)

	err := p.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM outbox WHERE published_at IS NULL),
		  -- Возраст самого старого неопубликованного: пусто — значит ноль,
		  -- очередь разобрана.
		  COALESCE((SELECT EXTRACT(EPOCH FROM now() - min(created_at))
		              FROM outbox WHERE published_at IS NULL), 0),
		  -- Выбывшие в парк не входят: списанная машина не должна ни
		  -- увеличивать поголовье, ни числиться молчащей, ни делать вид,
		  -- что от неё давно не было отчётов.
		  (SELECT count(*) FROM hosts WHERE retired_at IS NULL),
		  -- Молчащие берутся по отметке, которую ставит сама служба, а не
		  -- пересчитываются здесь по порогу: иначе два места считали бы одно
		  -- и то же по-разному, и в какой-то момент график разошёлся бы с
		  -- событиями в ленте.
		  (SELECT count(*) FROM hosts WHERE silent_since IS NOT NULL AND retired_at IS NULL),
		  COALESCE((SELECT EXTRACT(EPOCH FROM now() - min(last_report_at))
		              FROM hosts WHERE silent_since IS NULL AND retired_at IS NULL), 0),
		  -- Последний отчёт вообще: по нему видно, работает ли приём.
		  COALESCE((SELECT EXTRACT(EPOCH FROM now() - max(last_report_at))
		              FROM hosts WHERE retired_at IS NULL), 0),
		  (SELECT count(*) FROM unresolved_reports WHERE resolved_at IS NULL),
		  (SELECT count(*) FROM commands WHERE done_at IS NULL AND expires_at > now())`,
	).Scan(
		&s.OutboxPending, &lagSeconds,
		&s.Hosts, &s.HostsSilent, &oldestSecs, &lastSecs,
		&s.Unresolved, &s.Commands,
	)
	if err != nil {
		return Stats{}, fmt.Errorf("показатели службы: %w", err)
	}

	s.OutboxLag = time.Duration(lagSeconds * float64(time.Second))
	s.OldestReport = time.Duration(oldestSecs * float64(time.Second))
	s.LastReport = time.Duration(lastSecs * float64(time.Second))

	return s, nil
}
