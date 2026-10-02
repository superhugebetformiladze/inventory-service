package silence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

var сейчас = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

// хранилищеПамяти изображает базу: помнит, кого уже пометили молчащим.
type хранилищеПамяти struct {
	машины   []storage.SilentHost
	помечены map[uuid.UUID]bool
	messages []storage.OutboxMessage
	порог    time.Time
	err      error
}

func новоеХранилище(машины ...storage.SilentHost) *хранилищеПамяти {
	return &хранилищеПамяти{машины: машины, помечены: map[uuid.UUID]bool{}}
}

func (х *хранилищеПамяти) MarkSilent(
	_ context.Context, silentBefore, _ time.Time, limit int,
	build func(storage.SilentHost) ([]storage.OutboxMessage, error),
) ([]storage.SilentHost, error) {
	if х.err != nil {
		return nil, х.err
	}
	х.порог = silentBefore

	var found []storage.SilentHost
	for _, h := range х.машины {
		if len(found) >= limit {
			break
		}
		// Ровно то, что делает частичный индекс в базе: помеченные не
		// попадают в выборку вовсе.
		if х.помечены[h.ID] || !h.LastReport.Before(silentBefore) {
			continue
		}
		msgs, err := build(h)
		if err != nil {
			return nil, err
		}
		х.messages = append(х.messages, msgs...)
		х.помечены[h.ID] = true
		found = append(found, h)
	}
	return found, nil
}

func наблюдатель(х *хранилищеПамяти, порог time.Duration) *Watcher {
	return New(х, Options{
		Threshold: порог,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       func() time.Time { return сейчас },
	})
}

func машина(имя string, молчитДней int) storage.SilentHost {
	return storage.SilentHost{
		ID:         uuid.New(),
		Hostname:   имя,
		LastReport: сейчас.AddDate(0, 0, -молчитДней),
	}
}

func TestМолчащаяМашинаПорождаетСобытие(t *testing.T) {
	х := новоеХранилище(машина("PC-УЕХАЛ", 10))

	n, err := наблюдатель(х, 7*24*time.Hour).Tick(t.Context())
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if n != 1 {
		t.Fatalf("замечено машин: %d, ожидалась одна", n)
	}

	var event contract.Event
	if err := json.Unmarshal(х.messages[0].Payload, &event); err != nil {
		t.Fatalf("событие не разбирается: %v", err)
	}
	if event.Type != contract.EventSilent {
		t.Errorf("вид события %q, ожидался %q", event.Type, contract.EventSilent)
	}
	if event.HostID == "" {
		t.Error("событию о молчании нужен идентификатор машины")
	}
	if event.Reason == "" {
		t.Error("человеку нужно знать, сколько машина молчит")
	}
	if х.messages[0].Topic != publish.TopicEvents {
		t.Errorf("событие ушло в топик %q", х.messages[0].Topic)
	}
	if х.messages[0].Key != event.HostID {
		t.Error("ключ сообщения должен быть идентификатором машины (I4)")
	}
}

func TestСобытиеОМолчанииУходитОдинРаз(t *testing.T) {
	// Без пометки событие уходило бы на каждом проходе задачи, пока машина
	// стоит выключенная в отпуске владельца, — это несколько сотен сообщений
	// об одном и том же факте.
	х := новоеХранилище(машина("PC-В-ОТПУСКЕ", 30))
	w := наблюдатель(х, 7*24*time.Hour)

	if n, _ := w.Tick(t.Context()); n != 1 {
		t.Fatalf("первый проход должен заметить машину, замечено %d", n)
	}
	for range 5 {
		if n, _ := w.Tick(t.Context()); n != 0 {
			t.Fatalf("повторный проход не должен порождать событий, замечено %d", n)
		}
	}
	// Два сообщения за один проход, а не за каждый: событие тем, кто слушает
	// ленту сейчас, и состояние тем, кто прочитает уплотняемый топик потом.
	if len(х.messages) != 2 {
		t.Errorf("сообщений %d, ожидалось два за единственный проход", len(х.messages))
	}
}

func TestОМолчанииУходитИСобытие_ИСостояние(t *testing.T) {
	// Одного события мало: оно уходит один раз, и потребитель, поднятый
	// через месяц, его уже не увидит. Машина, замолчавшая прошлой весной,
	// выглядела бы для него совершенно здоровой.
	х := новоеХранилище(машина("PC-ЗАБЫТЫЙ", 30))

	if n, err := наблюдатель(х, 7*24*time.Hour).Tick(t.Context()); err != nil || n != 1 {
		t.Fatalf("замечено %d машин (ошибка %v)", n, err)
	}

	топики := map[string]int{}
	for _, m := range х.messages {
		топики[m.Topic]++
		if m.Key != х.машины[0].ID.String() {
			t.Error("ключ сообщения должен быть идентификатором машины (I4)")
		}
	}
	if топики[publish.TopicEvents] != 1 {
		t.Errorf("событий о молчании: %d", топики[publish.TopicEvents])
	}
	if топики[publish.TopicHosts] != 1 {
		t.Errorf("записей состояния: %d", топики[publish.TopicHosts])
	}

	var state contract.HostState
	for _, m := range х.messages {
		if m.Topic == publish.TopicHosts {
			if err := json.Unmarshal(m.Payload, &state); err != nil {
				t.Fatalf("разбор состояния: %v", err)
			}
		}
	}
	if state.SilentSince == nil {
		t.Error("в состоянии не отмечено, с какого момента машина молчит")
	}
}

func TestМашинаНаСвязиНеСчитаетсяМолчащей(t *testing.T) {
	х := новоеХранилище(машина("PC-РАБОТАЕТ", 1))

	if n, err := наблюдатель(х, 7*24*time.Hour).Tick(t.Context()); err != nil || n != 0 {
		t.Fatalf("замечено %d машин (ошибка %v), ожидалось 0", n, err)
	}
}

func TestПорогОтсчитываетсяОтТекущегоВремени(t *testing.T) {
	х := новоеХранилище()

	if _, err := наблюдатель(х, 3*24*time.Hour).Tick(t.Context()); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	want := сейчас.Add(-3 * 24 * time.Hour)
	if !х.порог.Equal(want) {
		t.Errorf("порог %v, ожидался %v", х.порог, want)
	}
}

func TestОшибкаБазыНеРоняетНаблюдателя(t *testing.T) {
	// Задача крутится часами; падение процесса из-за одной неудачной выборки
	// остановило бы и релей, и приём отчётов.
	х := новоеХранилище()
	х.err = errors.New("база недоступна")

	if _, err := наблюдатель(х, time.Hour).Tick(t.Context()); err == nil {
		t.Fatal("ошибка должна возвращаться вызывающему")
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		наблюдатель(х, time.Hour).Run(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("наблюдатель не остановился по отмене контекста")
	}
}
