// Package ingest — приём отчёта от агента.
//
// Здесь заканчивается недоверие к содержимому и начинается запись. Всё, что
// приходит снаружи, проверяется контрактом до того, как попадёт в базу.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// ErrInvalid — отчёт не прошёл проверку контрактом.
var ErrInvalid = errors.New("отчёт негоден")

// Store — то, что приёму нужно от базы.
type Store interface {
	SaveSnapshot(
		ctx context.Context,
		snap contract.Snapshot,
		receivedAt time.Time,
		agentUUID *uuid.UUID,
		build storage.MessageBuilder,
	) (storage.SavedSnapshot, error)
}

// Service принимает отчёты.
type Service struct {
	store Store
	now   func() time.Time
}

// New создаёт службу приёма. now подменяется в тестах.
func New(store Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}

// Result — что произошло с отчётом.
type Result struct {
	HostID     uuid.UUID
	SnapshotID uuid.UUID
	Duplicate  bool
	FirstSeen  bool

	// Unresolved — машину опознать не удалось, отчёт отложен человеку.
	Unresolved bool
	Reason     string
}

// Accept принимает отчёт: проверяет, нормализует и записывает вместе с
// сообщениями для ленты одной транзакцией.
func (s *Service) Accept(ctx context.Context, snap contract.Snapshot, agentUUID *uuid.UUID) (Result, error) {
	now := s.now().UTC()

	if err := snap.Validate(now); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	normalize(&snap)

	saved, err := s.store.SaveSnapshot(ctx, snap, now, agentUUID,
		func(saved storage.SavedSnapshot) ([]storage.OutboxMessage, error) {
			return buildMessages(snap, saved, now)
		})
	if err != nil {
		return Result{}, err
	}

	return Result{
		HostID:     saved.HostID,
		SnapshotID: saved.SnapshotID,
		Duplicate:  saved.Duplicate,
		FirstSeen:  saved.FirstSeen,
		Unresolved: saved.Unresolved,
		Reason:     saved.Reason,
	}, nil
}

// normalize приводит к каноническому виду то, от чего зависит опознание.
//
// Агент это уже сделал, но служба повторяет: доверять чужой нормализации в
// вопросе, от которого зависит склейка карточек, нельзя. Агент старой версии
// или самописный отправитель пришлют «AA-BB-CC», и по нему заведётся вторая
// машина.
func normalize(snap *contract.Snapshot) {
	snap.CollectedAt = snap.CollectedAt.UTC()
	for i := range snap.Interfaces {
		snap.Interfaces[i].MAC = contract.NormalizeMAC(snap.Interfaces[i].MAC)
	}
}

// buildMessages готовит сообщения ленты по итогам приёма.
//
// Событий об изменениях здесь ещё нет: сравнивать не с чем, пока не написан
// дифф (этап 5). Публиковать «изменилось» на каждый отчёт было бы нарушением
// I9 с первого же дня.
func buildMessages(
	snap contract.Snapshot, saved storage.SavedSnapshot, now time.Time,
) ([]storage.OutboxMessage, error) {
	if saved.Unresolved {
		// Повторный отчёт той же неопознанной машины события не порождает.
		//
		// Инвариант I9: событие — только при фактическом расхождении.
		// Машина, которую не разобрали, приходит каждые четыре часа, и
		// событие на каждый её приход — это ровно тот шум, из-за которого
		// ленту событий перестают читать.
		if saved.UnresolvedRepeat {
			return nil, nil
		}

		// Машины нет и не будет, пока человек не разберётся. Событие нужно
		// потребителю не ради данных, а ради того, чтобы он показал очередь
		// разбора: иначе отчёты копятся там, куда никто не смотрит.
		event := contract.Event{
			SchemaVersion: contract.SchemaVersion,
			Type:          contract.EventUnresolved,
			OccurredAt:    now,
			Reason:        saved.Reason,
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("сериализация события: %w", err)
		}

		return []storage.OutboxMessage{{
			Topic: publish.TopicEvents,
			// Ключ — отметка агента, а не машины: машины ещё нет. Порядок
			// внутри одной неопознанной машины при этом сохраняется.
			Key:     snap.AgentID,
			Payload: payload,
		}}, nil
	}

	envelope := struct {
		SchemaVersion int               `json:"schema_version"`
		HostID        string            `json:"host_id"`
		SnapshotID    string            `json:"snapshot_id"`
		Snapshot      contract.Snapshot `json:"snapshot"`
	}{
		SchemaVersion: contract.SchemaVersion,
		HostID:        saved.HostID.String(),
		SnapshotID:    saved.SnapshotID.String(),
		Snapshot:      snap,
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("сериализация сообщения: %w", err)
	}

	messages := []storage.OutboxMessage{{
		Topic:   publish.TopicSnapshots,
		Key:     saved.HostID.String(), // инвариант I4
		Payload: payload,
	}}

	// Событие — только на первое появление или на фактическое расхождение
	// (инвариант I9). Совпавший отчёт двигает отметку «на связи» и больше
	// ничего: девятнадцать отчётов из двадцати ничем не отличаются от
	// предыдущего, и событие на каждом утопило бы настоящие изменения.
	var event *contract.Event
	switch {
	case saved.FirstSeen:
		event = &contract.Event{
			SchemaVersion: contract.SchemaVersion,
			Type:          contract.EventFirstSeen,
			HostID:        saved.HostID.String(),
			SnapshotID:    saved.SnapshotID.String(),
			OccurredAt:    now,
		}
	case len(saved.Changes) > 0:
		event = &contract.Event{
			SchemaVersion: contract.SchemaVersion,
			Type:          contract.EventChanged,
			HostID:        saved.HostID.String(),
			SnapshotID:    saved.SnapshotID.String(),
			OccurredAt:    now,
			Changes:       saved.Changes,
		}
	}

	// Возвращение на связь — тоже повод обновить состояние, даже когда в
	// машине ничего не изменилось.
	//
	// Пометка о молчании уже разошлась по потребителям, и снять её может
	// только новая запись в уплотняемом топике. Без этого машина, которую на
	// две недели увезли в отпуск, вернётся, будет исправно отчитываться — и
	// останется в учёте помеченной как молчащая до первой замены планки
	// памяти.
	if event == nil && !saved.Returned {
		return messages, nil
	}

	if event != nil {
		eventPayload, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("сериализация события: %w", err)
		}
		messages = append(messages, storage.OutboxMessage{
			Topic:   publish.TopicEvents,
			Key:     saved.HostID.String(),
			Payload: eventPayload,
		})
	}

	// Состояние машины в уплотняемый топик — при изменении или возвращении
	// на связь.
	//
	// Уплотняемый топик хранит последнюю запись по каждому ключу навсегда, и
	// новый потребитель читает его целиком, чтобы построить картину парка с
	// нуля. Писать в него на каждый отчёт значило бы гонять пятьсот
	// одинаковых сообщений в час ради состояния, которое не менялось.
	state := contract.HostState{
		SchemaVersion: contract.SchemaVersion,
		HostID:        saved.HostID.String(),
		FirstSeenAt:   saved.FirstSeenAt,
		LastReportAt:  now,
		Snapshot:      snap,
	}
	statePayload, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("сериализация состояния: %w", err)
	}
	messages = append(messages, storage.OutboxMessage{
		Topic:   publish.TopicHosts,
		Key:     saved.HostID.String(),
		Payload: statePayload,
	})

	return messages, nil
}
