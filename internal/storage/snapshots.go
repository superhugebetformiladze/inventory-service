package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/broton07/inventory_service/internal/identity"
	"github.com/broton07/inventory_service/pkg/contract"
)

// SavedSnapshot — результат приёма отчёта.
type SavedSnapshot struct {
	HostID     uuid.UUID
	SnapshotID uuid.UUID

	// Duplicate — отчёт с таким временем сбора уже принимался. Это не ошибка:
	// агент повторяет отправку при обрыве связи (инвариант I3).
	Duplicate bool

	// FirstSeen — машина заведена только что.
	FirstSeen bool

	// Unresolved — машину опознать не удалось. Отчёт сохранён в очередь
	// разбора, данные машины не тронуты, HostID пуст.
	Unresolved bool

	// UnresolvedID и Reason заполнены при Unresolved.
	UnresolvedID uuid.UUID

	// UnresolvedRepeat — та же неопознанная машина пришла снова. Событие о
	// ней уже публиковалось, и повторять его — это ровно тот шум, против
	// которого заведён инвариант I9.
	UnresolvedRepeat bool
	Reason           string

	// Changes — что изменилось со времени прошлого отчёта. Пусто в самом
	// частом случае: девятнадцать отчётов из двадцати ничем не отличаются от
	// предыдущего.
	Changes []contract.Change

	// Snapshot — принятый снимок. Нужен сборщику сообщений, чтобы положить
	// состояние машины в уплотняемый топик.
	Snapshot contract.Snapshot

	// Returned — машина числилась молчащей и вернулась на связь.
	//
	// Состояние в уплотняемый топик надо положить и тогда, когда ничего не
	// изменилось: пометка о молчании уже разошлась по потребителям, и снять
	// её может только новое состояние. Без этого машина, замолчавшая в
	// отпуске владельца, останется в учёте молчащей навсегда.
	Returned bool

	// FirstSeenAt — когда машину завели. Нужно тому же состоянию.
	FirstSeenAt time.Time
}

// OutboxMessage — сообщение, которое релей отправит в ленту после коммита.
type OutboxMessage struct {
	Topic   string
	Key     string
	Payload []byte
}

// MessageBuilder собирает сообщения ленты по итогам приёма.
type MessageBuilder func(SavedSnapshot) ([]OutboxMessage, error)

// SaveSnapshot опознаёт машину, записывает снимок и сообщения для ленты ОДНОЙ
// транзакцией (инвариант I2).
//
// Порядок здесь и есть смысл всего механизма: пока сообщение лежит в той же
// транзакции, что и данные, невозможен ни один из двух плохих исходов —
// «данные записаны, событие потеряно» и «событие ушло, данных нет».
func (p *Pool) SaveSnapshot(
	ctx context.Context,
	snap contract.Snapshot,
	receivedAt time.Time,
	agentUUID *uuid.UUID,
	build MessageBuilder,
) (SavedSnapshot, error) {
	payload, err := json.Marshal(snap)
	if err != nil {
		return SavedSnapshot{}, fmt.Errorf("сериализация снимка: %w", err)
	}
	hash := sha256.Sum256(payload)

	var out SavedSnapshot

	err = pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		ids := identity.Identifiers(snap)

		known, err := lookupIdentifiers(ctx, tx, ids)
		if err != nil {
			return err
		}

		decision := identity.Decide(ids, known)

		if decision.Verdict == identity.VerdictAmbiguous {
			// Инвариант I8: запись не создаётся и не переписывается.
			// Единственное, что происходит, — отчёт откладывается человеку.
			id, attempts, err := saveUnresolved(ctx, tx, agentUUID,
				decision.Reason, decision.Candidates, payload, receivedAt)
			if err != nil {
				return err
			}

			out = SavedSnapshot{
				Unresolved:   true,
				UnresolvedID: id,
				Reason:       decision.Reason,
				// Повтор: та же машина приходит снова, пока её не разобрали.
				// Событие о ней уже уходило.
				UnresolvedRepeat: attempts > 1,
			}
			return buildAndInsert(ctx, tx, build, out)
		}

		hostID, touch, err := applyDecision(ctx, tx, decision, snap, receivedAt)
		if err != nil {
			return err
		}
		out.HostID = hostID
		out.Returned = touch.Returned
		out.FirstSeenAt = touch.FirstSeen

		// Кто сейчас отвечает за машину. Через эту связь человек ставит
		// задание «собери с ПК-214», не зная отметок агентов.
		if err := setHostAgent(ctx, tx, hostID, agentUUID); err != nil {
			return err
		}
		out.FirstSeen = decision.Verdict == identity.VerdictNew

		snapshotID := uuid.New()
		err = tx.QueryRow(ctx, `
			INSERT INTO snapshots (id, host_id, collected_at, received_at, origin, agent_version, payload, payload_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT ON CONSTRAINT snapshots_host_collected_key DO NOTHING
			RETURNING id`,
			snapshotID, hostID, snap.CollectedAt, receivedAt,
			string(snap.Origin), snap.AgentVersion, payload, hash[:],
		).Scan(&snapshotID)

		if errors.Is(err, pgx.ErrNoRows) {
			// Повтор. Сообщения в ленту не пишем: событие по этому снимку уже
			// опубликовано, а второе такое же заставило бы потребителя
			// обрабатывать одно изменение дважды.
			out.Duplicate = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("вставка снимка: %w", err)
		}
		out.SnapshotID = snapshotID
		out.Snapshot = snap

		changes, err := detectChanges(ctx, tx, hostID, snapshotID, snap, out.FirstSeen, receivedAt)
		if err != nil {
			return err
		}
		out.Changes = changes

		return buildAndInsert(ctx, tx, build, out)
	})

	if err != nil {
		return SavedSnapshot{}, err
	}
	return out, nil
}

// applyDecision заводит или находит машину и закрепляет за ней новые признаки.
func applyDecision(
	ctx context.Context, tx pgx.Tx,
	decision identity.Decision, snap contract.Snapshot, now time.Time,
) (uuid.UUID, HostTouch, error) {
	var (
		hostID uuid.UUID
		touch  HostTouch
	)

	if decision.Verdict == identity.VerdictNew {
		id, err := createHost(ctx, tx, snap.AgentID, snap.Machine.Hostname, now)
		if err != nil {
			return uuid.Nil, HostTouch{}, err
		}
		hostID = id
		touch.FirstSeen = now
	} else {
		parsed, err := uuid.Parse(string(decision.Host))
		if err != nil {
			return uuid.Nil, HostTouch{}, fmt.Errorf("непонятный идентификатор машины %q: %w", decision.Host, err)
		}
		hostID = parsed

		touch, err = touchHost(ctx, tx, hostID, snap.AgentID, snap.Machine.Hostname, now)
		if err != nil {
			return uuid.Nil, HostTouch{}, err
		}
	}

	// Так машина, опознанная по заводскому номеру после переустановки,
	// получает новую отметку агента и дальше находится по ней сразу.
	if err := attachIdentifiers(ctx, tx, hostID, decision.Learn, now); err != nil {
		return uuid.Nil, HostTouch{}, err
	}

	return hostID, touch, nil
}

func buildAndInsert(ctx context.Context, tx pgx.Tx, build MessageBuilder, saved SavedSnapshot) error {
	if build == nil {
		return nil
	}

	messages, err := build(saved)
	if err != nil {
		return fmt.Errorf("подготовка сообщений: %w", err)
	}

	for _, m := range messages {
		if _, err := tx.Exec(ctx,
			`INSERT INTO outbox (topic, key, payload) VALUES ($1, $2, $3)`,
			m.Topic, m.Key, m.Payload); err != nil {
			return fmt.Errorf("запись в outbox: %w", err)
		}
	}
	return nil
}

// LastSnapshot возвращает последний снимок машины. Понадобится на этапе 5 для
// сравнения.
func (p *Pool) LastSnapshot(ctx context.Context, hostID uuid.UUID) (contract.Snapshot, error) {
	var payload []byte

	err := p.QueryRow(ctx, `
		SELECT payload FROM snapshots
		 WHERE host_id = $1
		 ORDER BY collected_at DESC
		 LIMIT 1`, hostID).Scan(&payload)

	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Snapshot{}, ErrNotFound
	}
	if err != nil {
		return contract.Snapshot{}, err
	}

	var snap contract.Snapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return contract.Snapshot{}, fmt.Errorf("разбор сохранённого снимка: %w", err)
	}
	return snap, nil
}

// ErrNotFound — запись не найдена.
var ErrNotFound = errors.New("не найдено")
