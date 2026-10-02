// Package publish отправляет сообщения в ленту.
package publish

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Топики ленты. Имена с версией в конце: несовместимое изменение контракта
// заводит соседний топик, а не ломает читателей текущего (инвариант I5).
const (
	TopicSnapshots = "inventory.snapshots.v1"
	TopicHosts     = "inventory.hosts.v1"
	TopicEvents    = "inventory.events.v1"
)

// Message — одно сообщение ленты.
type Message struct {
	Topic string

	// Key — идентификатор машины (инвариант I4). Он определяет партицию, а
	// значит порядок: все сообщения одной машины идут по порядку, разные
	// машины обрабатываются параллельно. Глобального порядка нет и не нужно.
	Key string

	Value []byte
}

// Publisher отправляет сообщения в ленту.
type Publisher interface {
	Publish(ctx context.Context, msgs []Message) error
	Close()
}

// Kafka — отправитель поверх franz-go.
type Kafka struct {
	client *kgo.Client
}

// NewKafka подключается к брокерам.
func NewKafka(brokers []string) (*Kafka, error) {
	if len(brokers) == 0 {
		return nil, errors.New("не задан ни один брокер")
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),

		// Подтверждение от всех реплик. Медленнее, чем от одной, но отчёт,
		// подтверждённый лидером и потерянный при его падении, — это ровно
		// та потеря, ради предотвращения которой заведён outbox.
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),

		// Без идемпотентности брокер может записать дубль при внутреннем
		// повторе — здесь его отключать нечем и незачем.
		kgo.ProducerLinger(5*time.Millisecond),
	)
	if err != nil {
		return nil, fmt.Errorf("подключение к ленте: %w", err)
	}

	return &Kafka{client: client}, nil
}

// Publish отправляет пачку и ждёт подтверждения по всей пачке.
//
// Ждём намеренно: вызывающий — релей outbox, и он имеет право пометить
// сообщения отправленными только после подтверждения. Асинхронная отправка
// здесь означала бы «пометил отправленным то, что ещё в буфере».
func (k *Kafka) Publish(ctx context.Context, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}

	records := make([]*kgo.Record, len(msgs))
	for i, m := range msgs {
		records[i] = &kgo.Record{
			Topic: m.Topic,
			Key:   []byte(m.Key),
			Value: m.Value,
		}
	}

	results := k.client.ProduceSync(ctx, records...)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("отправка %d сообщений: %w", len(msgs), err)
	}
	return nil
}

// Close закрывает клиента, дождавшись отправки того, что в буфере.
func (k *Kafka) Close() {
	k.client.Close()
}

// Ping проверяет, что брокеры отвечают. Используется в /ready.
func (k *Kafka) Ping(ctx context.Context) error {
	return k.client.Ping(ctx)
}
