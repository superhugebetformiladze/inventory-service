package outbox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/storage"
)

// хранилищеПамяти изображает outbox без базы.
type хранилищеПамяти struct {
	mu       sync.Mutex
	queue    []storage.PendingMessage
	failures []string
}

func (х *хранилищеПамяти) добавить(msgs ...storage.PendingMessage) {
	х.mu.Lock()
	defer х.mu.Unlock()
	х.queue = append(х.queue, msgs...)
}

func (х *хранилищеПамяти) ProcessPending(
	ctx context.Context,
	limit int,
	publish func(context.Context, []storage.PendingMessage) error,
) (int, error) {
	х.mu.Lock()
	defer х.mu.Unlock()

	if len(х.queue) == 0 {
		return 0, nil
	}
	batch := х.queue
	if len(batch) > limit {
		batch = batch[:limit]
	}

	if err := publish(ctx, batch); err != nil {
		// Транзакция откатилась бы — очередь не трогаем. Это и есть то
		// свойство, ради которого outbox существует.
		return 0, err
	}

	х.queue = х.queue[len(batch):]
	return len(batch), nil
}

func (х *хранилищеПамяти) NoteFailure(_ context.Context, reason string, _ int) error {
	х.mu.Lock()
	defer х.mu.Unlock()
	х.failures = append(х.failures, reason)
	return nil
}

func (х *хранилищеПамяти) осталось() int {
	х.mu.Lock()
	defer х.mu.Unlock()
	return len(х.queue)
}

// отправительПамяти изображает ленту.
type отправительПамяти struct {
	mu       sync.Mutex
	sent     []publish.Message
	failWith error
}

func (о *отправительПамяти) Publish(_ context.Context, msgs []publish.Message) error {
	о.mu.Lock()
	defer о.mu.Unlock()
	if о.failWith != nil {
		return о.failWith
	}
	о.sent = append(о.sent, msgs...)
	return nil
}

func (о *отправительПамяти) Close() {}

func (о *отправительПамяти) отправлено() []publish.Message {
	о.mu.Lock()
	defer о.mu.Unlock()
	return append([]publish.Message(nil), о.sent...)
}

func тихийРелей(store Store, pub publish.Publisher) *Relay {
	return New(store, pub, Options{
		Interval:  time.Millisecond,
		BatchSize: 10,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestРелейПереноситСообщенияВЛенту(t *testing.T) {
	store := &хранилищеПамяти{}
	store.добавить(
		storage.PendingMessage{ID: 1, Topic: "t", Key: "host-1", Payload: []byte(`{"a":1}`)},
		storage.PendingMessage{ID: 2, Topic: "t", Key: "host-2", Payload: []byte(`{"a":2}`)},
	)
	pub := &отправительПамяти{}

	n, err := тихийРелей(store, pub).Tick(t.Context())
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if n != 2 {
		t.Fatalf("ожидалось 2 сообщения, обработано %d", n)
	}
	if len(pub.отправлено()) != 2 {
		t.Fatalf("в ленту ушло %d сообщений", len(pub.отправлено()))
	}
	if store.осталось() != 0 {
		t.Errorf("очередь должна опустеть, осталось %d", store.осталось())
	}
}

func TestНедоступнаяЛентаНеТеряетСообщения(t *testing.T) {
	// Ровно то свойство, ради которого outbox и существует: пока лента лежит,
	// сообщения ждут в базе, а не исчезают в буфере упавшего процесса.
	store := &хранилищеПамяти{}
	store.добавить(storage.PendingMessage{ID: 1, Topic: "t", Key: "host-1", Payload: []byte(`{}`)})

	pub := &отправительПамяти{failWith: errors.New("брокер недоступен")}
	relay := тихийРелей(store, pub)

	if _, err := relay.Tick(t.Context()); err == nil {
		t.Fatal("ожидалась ошибка отправки")
	}
	if store.осталось() != 1 {
		t.Fatalf("сообщение должно остаться в очереди, осталось %d", store.осталось())
	}
	if len(store.failures) != 1 {
		t.Errorf("причина неудачи должна быть записана, записей %d", len(store.failures))
	}

	// Лента поднялась — сообщение уходит без вмешательства человека.
	pub.mu.Lock()
	pub.failWith = nil
	pub.mu.Unlock()

	if _, err := relay.Tick(t.Context()); err != nil {
		t.Fatalf("после восстановления ленты ошибки быть не должно: %v", err)
	}
	if store.осталось() != 0 {
		t.Errorf("очередь должна опуститься, осталось %d", store.осталось())
	}
}

func TestПорядокСообщенийСохраняется(t *testing.T) {
	// Потребитель, получивший «стало 16 ГБ» раньше, чем «стало 8 ГБ», запишет
	// в карточку неправильное значение и не узнает об этом.
	store := &хранилищеПамяти{}
	for i := int64(1); i <= 5; i++ {
		store.добавить(storage.PendingMessage{
			ID: i, Topic: "t", Key: "host-1", Payload: []byte{byte('0' + i)},
		})
	}
	pub := &отправительПамяти{}

	if _, err := тихийРелей(store, pub).Tick(t.Context()); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	sent := pub.отправлено()
	for i, m := range sent {
		want := byte('0' + i + 1)
		if m.Value[0] != want {
			t.Fatalf("порядок нарушен на позиции %d: %q вместо %q", i, m.Value[0], want)
		}
	}
}

func TestОтсрочкаРастётПокаЛентаЛежит(t *testing.T) {
	// Без роста паузы упавшая Kafka означала бы запрос к базе и попытку
	// подключения каждую секунду от каждого экземпляра службы — то есть
	// добивание того, что и так лежит.
	relay := тихийРелей(&хранилищеПамяти{}, &отправительПамяти{})
	relay.interval = time.Second
	relay.maxBackoff = 8 * time.Second

	ожидания := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, want := range ожидания {
		if got := relay.nextBackoff(); got != want {
			t.Fatalf("попытка %d: пауза %v, ожидалась %v", i+1, got, want)
		}
	}
}

func TestПустаяОчередьНеОшибка(t *testing.T) {
	n, err := тихийРелей(&хранилищеПамяти{}, &отправительПамяти{}).Tick(t.Context())
	if err != nil {
		t.Fatalf("пустая очередь не должна быть ошибкой: %v", err)
	}
	if n != 0 {
		t.Errorf("обработано %d сообщений при пустой очереди", n)
	}
}

func TestОстановкаПоКонтексту(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	relay := тихийРелей(&хранилищеПамяти{}, &отправительПамяти{})

	done := make(chan struct{})
	go func() {
		relay.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("релей не остановился по отмене контекста")
	}
}
