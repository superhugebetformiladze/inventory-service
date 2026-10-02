package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/broton07/inventory_service/pkg/contract"
)

// CommandLease — сколько задание считается «на руках» у агента.
//
// Агент мог упасть, потерять сеть или быть выключенным сразу после получения.
// По истечении аренды задание снова становится доступным — иначе одно падение
// агента означало бы навсегда потерянное «собрать сейчас», и человек ждал бы
// ответа, которого не будет.
const CommandLease = 5 * time.Minute

// CommandRow — задание в базе.
type CommandRow struct {
	ID        uuid.UUID
	AgentUUID uuid.UUID
	HostID    *uuid.UUID
	Kind      contract.CommandKind
	Params    map[string]any
	CreatedAt time.Time
	CreatedBy string
	ExpiresAt time.Time
	TakenAt   *time.Time
	Attempts  int
	DoneAt    *time.Time
	OK        *bool
	Error     string

	// Hostname заполняется только в списках — для человека, который читает
	// очередь заданий и не помнит идентификаторов машин.
	Hostname string
}

// CreateCommand ставит задание в очередь.
func (p *Pool) CreateCommand(
	ctx context.Context,
	agentUUID uuid.UUID, hostID *uuid.UUID,
	kind contract.CommandKind, params map[string]any,
	createdBy string, expiresAt time.Time,
) (uuid.UUID, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return uuid.Nil, fmt.Errorf("параметры задания: %w", err)
	}

	id := uuid.New()
	if _, err := p.Exec(ctx, `
		INSERT INTO commands (id, agent_uuid, host_id, kind, params, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, agentUUID, hostID, string(kind), encoded, createdBy, expiresAt); err != nil {
		return uuid.Nil, fmt.Errorf("постановка задания: %w", err)
	}
	return id, nil
}

// TakeCommands выдаёт агенту задания и помечает их выданными.
//
// Одним запросом, а не «выбрать, потом обновить»: два опроса от одного агента,
// пришедшие одновременно (агент перезапустился и старый процесс ещё жив),
// иначе получили бы одно и то же задание дважды.
//
// Условие выдачи читается так: не выполнено, не просрочено, и либо ещё никому
// не выдавалось, либо выдано давно и агент не отчитался.
func (p *Pool) TakeCommands(
	ctx context.Context, agentUUID uuid.UUID, now time.Time, limit int,
) ([]CommandRow, error) {
	rows, err := p.Query(ctx, `
		UPDATE commands
		   SET taken_at = $2,
		       attempts = attempts + 1
		 WHERE id IN (
		     SELECT id FROM commands
		      WHERE agent_uuid = $1
		        AND done_at IS NULL
		        AND expires_at > $2
		        AND (taken_at IS NULL OR taken_at < $3)
		      ORDER BY created_at
		      LIMIT $4
		      FOR UPDATE SKIP LOCKED
		 )
		RETURNING id, kind, params, created_at, attempts`,
		agentUUID, now, now.Add(-CommandLease), limit)
	if err != nil {
		return nil, fmt.Errorf("выдача заданий: %w", err)
	}
	defer rows.Close()

	var out []CommandRow
	for rows.Next() {
		var (
			c      CommandRow
			kind   string
			params []byte
		)
		if err := rows.Scan(&c.ID, &kind, &params, &c.CreatedAt, &c.Attempts); err != nil {
			return nil, err
		}
		c.Kind = contract.CommandKind(kind)
		if err := json.Unmarshal(params, &c.Params); err != nil {
			return nil, fmt.Errorf("разбор параметров задания: %w", err)
		}
		c.AgentUUID = agentUUID
		out = append(out, c)
	}
	return out, rows.Err()
}

// FinishCommand записывает результат выполнения.
//
// Проверка принадлежности агенту здесь, а не в вызывающем: без неё агент с
// действующим ключом мог бы закрыть чужое задание и оставить соседа ждать
// вечно.
func (p *Pool) FinishCommand(
	ctx context.Context, id, agentUUID uuid.UUID, ok bool, errText string, now time.Time,
) error {
	tag, err := p.Exec(ctx, `
		UPDATE commands
		   SET done_at = $3, ok = $4, error = $5
		 WHERE id = $1 AND agent_uuid = $2 AND done_at IS NULL`,
		id, agentUUID, now, ok, errText)
	if err != nil {
		return fmt.Errorf("запись результата задания: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AgentOfHost — агент, который сейчас отвечает за машину.
func (p *Pool) AgentOfHost(ctx context.Context, hostID uuid.UUID) (uuid.UUID, error) {
	var agentUUID *uuid.UUID

	err := p.QueryRow(ctx,
		`SELECT agent_uuid FROM hosts WHERE id = $1`, hostID).Scan(&agentUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("агент машины: %w", err)
	}
	if agentUUID == nil {
		// Машина известна, но её агент — нет. Так бывает у машин, перенесённых
		// из старой системы, и у тех, чей агент отозвали.
		return uuid.Nil, ErrNotFound
	}
	return *agentUUID, nil
}

// AgentByMark — агент по своей отметке.
func (p *Pool) AgentByMark(ctx context.Context, mark string) (uuid.UUID, error) {
	var id uuid.UUID

	err := p.QueryRow(ctx,
		`SELECT id FROM agents WHERE agent_id = $1 AND revoked_at IS NULL`, mark).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("агент по отметке: %w", err)
	}
	return id, nil
}

// HostByName — машина по имени. Для человека, который знает имя компьютера, а
// не его идентификатор.
func (p *Pool) HostByName(ctx context.Context, hostname string) (uuid.UUID, error) {
	var id uuid.UUID

	err := p.QueryRow(ctx,
		`SELECT id FROM hosts WHERE hostname = $1 ORDER BY last_report_at DESC LIMIT 1`,
		hostname).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("машина по имени: %w", err)
	}
	return id, nil
}

// ListCommands — последние задания, свежие сверху.
func (p *Pool) ListCommands(ctx context.Context, limit int) ([]CommandRow, error) {
	rows, err := p.Query(ctx, `
		SELECT c.id, c.kind, c.created_at, c.created_by, c.expires_at,
		       c.taken_at, c.attempts, c.done_at, c.ok, c.error,
		       COALESCE(h.hostname, '')
		  FROM commands c
		  LEFT JOIN hosts h ON h.id = c.host_id
		 ORDER BY c.created_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("список заданий: %w", err)
	}
	defer rows.Close()

	var out []CommandRow
	for rows.Next() {
		var (
			c    CommandRow
			kind string
		)
		if err := rows.Scan(&c.ID, &kind, &c.CreatedAt, &c.CreatedBy, &c.ExpiresAt,
			&c.TakenAt, &c.Attempts, &c.DoneAt, &c.OK, &c.Error, &c.Hostname); err != nil {
			return nil, err
		}
		c.Kind = contract.CommandKind(kind)
		out = append(out, c)
	}
	return out, rows.Err()
}

// setHostAgent запоминает, какой агент сейчас отвечает за машину.
func setHostAgent(ctx context.Context, tx pgx.Tx, hostID uuid.UUID, agentUUID *uuid.UUID) error {
	if agentUUID == nil {
		return nil
	}
	_, err := tx.Exec(ctx,
		`UPDATE hosts SET agent_uuid = $2 WHERE id = $1`, hostID, *agentUUID)
	if err != nil {
		return fmt.Errorf("привязка агента к машине: %w", err)
	}
	return nil
}
