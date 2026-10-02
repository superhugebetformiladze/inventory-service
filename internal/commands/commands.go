// Package commands — очередь заданий агентам.
//
// Задания ставит человек, забирает агент. Сервер сам к машине не стучится: она
// может быть дома, в командировке или просто выключена. Но причина важнее
// технической: если бы сервер умел отдавать команды пятистам компьютерам, то
// тот, кто однажды получит доступ к серверу, получил бы и всю сеть.
package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// ErrUnknownKind — вид задания не из закрытого списка (инвариант I6).
var ErrUnknownKind = errors.New("неизвестный вид задания")

// Store — то, что нужно от базы.
type Store interface {
	CreateCommand(ctx context.Context, agentUUID uuid.UUID, hostID *uuid.UUID,
		kind contract.CommandKind, params map[string]any,
		createdBy string, expiresAt time.Time) (uuid.UUID, error)
	TakeCommands(ctx context.Context, agentUUID uuid.UUID, now time.Time, limit int) ([]storage.CommandRow, error)
	FinishCommand(ctx context.Context, id, agentUUID uuid.UUID, ok bool, errText string, now time.Time) error
	AgentOfHost(ctx context.Context, hostID uuid.UUID) (uuid.UUID, error)
	Facts(ctx context.Context, hostID uuid.UUID) (storage.HostFacts, error)
}

// Service ведёт очередь заданий.
type Service struct {
	store Store
	ttl   time.Duration
	now   func() time.Time
}

// New создаёт службу.
func New(store Store, ttl time.Duration, now func() time.Time) *Service {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, ttl: ttl, now: now}
}

// Create ставит задание в очередь.
func (s *Service) Create(
	ctx context.Context,
	agentUUID uuid.UUID, hostID *uuid.UUID,
	kind contract.CommandKind, params map[string]any,
	createdBy string,
) (uuid.UUID, error) {
	now := s.now().UTC()

	// Проверяем тем же кодом, которым будет проверять агент: список видов
	// один на обе стороны и лежит в контракте (инвариант I6).
	candidate := contract.Command{
		SchemaVersion: contract.SchemaVersion,
		Kind:          kind,
		Params:        params,
		CreatedAt:     now,
	}
	if err := candidate.Validate(); err != nil {
		return uuid.Nil, fmt.Errorf("%w: %w", ErrUnknownKind, err)
	}

	return s.store.CreateCommand(ctx, agentUUID, hostID, kind, params, createdBy, now.Add(s.ttl))
}

// Take выдаёт агенту задания, помечая их выданными.
func (s *Service) Take(ctx context.Context, agentUUID uuid.UUID, limit int) ([]contract.Command, error) {
	rows, err := s.store.TakeCommands(ctx, agentUUID, s.now().UTC(), limit)
	if err != nil {
		return nil, err
	}

	out := make([]contract.Command, 0, len(rows))
	for _, r := range rows {
		out = append(out, contract.Command{
			SchemaVersion: contract.SchemaVersion,
			ID:            r.ID.String(),
			Kind:          r.Kind,
			Params:        r.Params,
			CreatedAt:     r.CreatedAt,
		})
	}
	return out, nil
}

// Finish записывает результат выполнения.
func (s *Service) Finish(
	ctx context.Context, id, agentUUID uuid.UUID, result contract.CommandResult,
) error {
	text := result.Error
	if len(text) > 500 {
		// Агент может прислать стектрейс целиком. В очереди заданий его никто
		// не читает, а место он занимает.
		text = text[:500] + "…"
	}
	return s.store.FinishCommand(ctx, id, agentUUID, result.OK, text, s.now().UTC())
}

// AgentOfHost — агент, который сейчас отчитывается за эту машину.
//
// Нужен постановке задания снаружи: человек в чужом интерфейсе смотрит на
// карточку компьютера и не знает ни про отметки агентов, ни про то, что их у
// машины за жизнь было несколько.
func (s *Service) AgentOfHost(ctx context.Context, hostID uuid.UUID) (uuid.UUID, error) {
	return s.store.AgentOfHost(ctx, hostID)
}

// Facts — сводка по машине для того, кто поставил задание и хочет знать, чем
// оно кончилось.
func (s *Service) Facts(ctx context.Context, hostID uuid.UUID) (storage.HostFacts, error) {
	return s.store.Facts(ctx, hostID)
}
