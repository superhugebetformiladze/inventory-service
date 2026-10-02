// Пакет retire выводит машину из учёта.
//
// Отдельным пакетом, а не строкой в команде: выбытие — это не удаление
// записи, а сообщение всем потребителям ленты, и собирать его в интерфейсе
// командной строки значило бы, что маршрут службы, когда он понадобится,
// соберёт его во второй раз и чуть иначе.
package retire

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// Store — то, что нужно от хранилища.
type Store interface {
	RetireHost(ctx context.Context, hostID uuid.UUID, now time.Time,
		build func(storage.Retired) (storage.OutboxMessage, error)) (storage.Retired, error)
	LastSnapshot(ctx context.Context, hostID uuid.UUID) (contract.Snapshot, error)
}

// Host выводит машину из учёта и отправляет о ней надгробие.
//
// Надгробие несёт последний снимок, а не пустоту: потребитель, который
// поднимется с нуля через полгода, должен узнать не только о том, что машины
// больше нет, но и о том, чем она была. Ради этого уплотняемый топик и
// существует.
func Host(ctx context.Context, store Store, hostID uuid.UUID, now time.Time) (storage.Retired, error) {
	// Снимок читается до транзакции: машины может уже не быть в базе, и
	// отсутствие снимка — не повод отказаться от надгробия.
	snap, err := store.LastSnapshot(ctx, hostID)
	if err != nil {
		snap = contract.Snapshot{}
	}

	return store.RetireHost(ctx, hostID, now, func(r storage.Retired) (storage.OutboxMessage, error) {
		state := contract.HostState{
			SchemaVersion: contract.SchemaVersion,
			HostID:        hostID.String(),
			LastReportAt:  now,
			Snapshot:      snap,
			Retired:       true,
		}
		payload, err := json.Marshal(state)
		if err != nil {
			return storage.OutboxMessage{}, fmt.Errorf("сериализация выбытия: %w", err)
		}
		return storage.OutboxMessage{
			Topic:   publish.TopicHosts,
			Key:     hostID.String(),
			Payload: payload,
		}, nil
	})
}
