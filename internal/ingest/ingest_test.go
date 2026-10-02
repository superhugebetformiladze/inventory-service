package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/publish"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

var сейчас = time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)

// хранилищеПамяти изображает базу и запоминает, что ушло бы в outbox.
type хранилищеПамяти struct {
	saved      contract.Snapshot
	messages   []storage.OutboxMessage
	duplicate  bool
	firstSeen  bool
	changes    []contract.Change
	unresolved string // непустое — машину опознать не удалось

	// unresolvedRepeat — та же неопознанная машина пришла снова.
	unresolvedRepeat bool
	err              error
	вызовов          int
	вернулась        bool
}

func (х *хранилищеПамяти) SaveSnapshot(
	_ context.Context,
	snap contract.Snapshot,
	_ time.Time,
	_ *uuid.UUID,
	build storage.MessageBuilder,
) (storage.SavedSnapshot, error) {
	х.вызовов++
	if х.err != nil {
		return storage.SavedSnapshot{}, х.err
	}
	х.saved = snap

	var saved storage.SavedSnapshot
	switch {
	case х.unresolved != "":
		saved = storage.SavedSnapshot{
			Unresolved:       true,
			UnresolvedID:     uuid.New(),
			Reason:           х.unresolved,
			UnresolvedRepeat: х.unresolvedRepeat,
		}
	case х.duplicate:
		// Повтор: сообщения не строятся вовсе — событие по этому снимку уже
		// опубликовано.
		return storage.SavedSnapshot{HostID: uuid.New(), Duplicate: true}, nil
	default:
		saved = storage.SavedSnapshot{
			HostID:     uuid.New(),
			SnapshotID: uuid.New(),
			FirstSeen:  х.firstSeen,
			Returned:   х.вернулась,
			Changes:    х.changes,
			Snapshot:   snap,
		}
	}

	msgs, err := build(saved)
	if err != nil {
		return storage.SavedSnapshot{}, err
	}
	х.messages = msgs

	return saved, nil
}

func годныйСнимок() contract.Snapshot {
	return contract.Snapshot{
		SchemaVersion: contract.SchemaVersion,
		AgentID:       "6f1e0a2c-7b4d-4a9e-9f31-0c2d5e8a1b77",
		AgentVersion:  "0.1.0",
		CollectedAt:   сейчас.Add(-time.Minute),
		Origin:        contract.OriginNetwork,
		Machine:       contract.Machine{Hostname: "PC-214-01", Serial: "7XKPL03"},
		Interfaces:    []contract.Interface{{Name: "Ethernet", MAC: "A4-BB-6D-11-22-33"}},
	}
}

func служба(store Store) *Service {
	return New(store, func() time.Time { return сейчас })
}

func TestГодныйОтчётПринимается(t *testing.T) {
	store := &хранилищеПамяти{}

	res, err := служба(store).Accept(t.Context(), годныйСнимок(), nil)
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if res.HostID == uuid.Nil {
		t.Error("машина не определена")
	}
	if len(store.messages) != 1 {
		t.Fatalf("в outbox должно уйти одно сообщение, ушло %d", len(store.messages))
	}
}

func TestНегодныйОтчётДоБазыНеДоходит(t *testing.T) {
	store := &хранилищеПамяти{}
	snap := годныйСнимок()
	snap.AgentID = ""

	_, err := служба(store).Accept(t.Context(), snap, nil)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
	}
	if store.вызовов != 0 {
		t.Error("негодный отчёт не должен доходить до базы")
	}
}

func TestАдресКартыНормализуетсяСлужбой(t *testing.T) {
	// Агент это уже сделал, но доверять чужой нормализации в вопросе, от
	// которого зависит склейка карточек, нельзя: агент старой версии или
	// самописный отправитель пришлют «AA-BB-CC», и заведётся вторая машина.
	store := &хранилищеПамяти{}

	if _, err := служба(store).Accept(t.Context(), годныйСнимок(), nil); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	if got := store.saved.Interfaces[0].MAC; got != "a4:bb:6d:11:22:33" {
		t.Errorf("адрес не приведён: %q", got)
	}
}

func TestКлючСообщенияЭтоИдентификаторМашины(t *testing.T) {
	// Инвариант I4: ключ определяет партицию, а значит порядок. Ключ,
	// выбранный иначе, развалил бы порядок событий по машине.
	store := &хранилищеПамяти{}

	res, err := служба(store).Accept(t.Context(), годныйСнимок(), nil)
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	msg := store.messages[0]
	if msg.Key != res.HostID.String() {
		t.Errorf("ключ %q, ожидался идентификатор машины %q", msg.Key, res.HostID)
	}
	if msg.Topic != publish.TopicSnapshots {
		t.Errorf("топик %q, ожидался %q", msg.Topic, publish.TopicSnapshots)
	}
}

func TestПовторНеПорождаетСообщенияВЛенту(t *testing.T) {
	// Агент повторяет отправку при обрыве связи. Второе такое же сообщение
	// заставило бы потребителя обрабатывать одно изменение дважды.
	store := &хранилищеПамяти{duplicate: true}

	res, err := служба(store).Accept(t.Context(), годныйСнимок(), nil)
	if err != nil {
		t.Fatalf("повтор не должен быть ошибкой: %v", err)
	}
	if !res.Duplicate {
		t.Error("повтор должен быть помечен как повтор")
	}
	if len(store.messages) != 0 {
		t.Errorf("на повтор не должно уходить сообщений, ушло %d", len(store.messages))
	}
}

func TestНоваяМашинаПорождаетСобытие(t *testing.T) {
	store := &хранилищеПамяти{firstSeen: true}

	if _, err := служба(store).Accept(t.Context(), годныйСнимок(), nil); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	if len(store.messages) != 3 {
		t.Fatalf("ожидались снимок, событие и состояние, сообщений %d", len(store.messages))
	}

	var event contract.Event
	if err := json.Unmarshal(store.messages[1].Payload, &event); err != nil {
		t.Fatalf("событие не разбирается: %v", err)
	}
	if event.Type != contract.EventFirstSeen {
		t.Errorf("вид события %q, ожидался %q", event.Type, contract.EventFirstSeen)
	}
}

func TestНеизменившийсяОтчётНеПорождаетСобытий(t *testing.T) {
	// Инвариант I9. Девятнадцать отчётов из двадцати ничем не отличаются от
	// предыдущего; событие на каждом утопило бы настоящие изменения.
	store := &хранилищеПамяти{}

	if _, err := служба(store).Accept(t.Context(), годныйСнимок(), nil); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	if len(store.messages) != 1 {
		t.Fatalf("ожидался только снимок, сообщений %d", len(store.messages))
	}
	if store.messages[0].Topic != publish.TopicSnapshots {
		t.Errorf("единственное сообщение ушло в топик %q", store.messages[0].Topic)
	}
}

func TestВозвращениеНаСвязьОбновляетСостояние(t *testing.T) {
	// Машина, которую увезли в отпуск на две недели, вернулась и отчиталась
	// ровно тем же составом. Изменений нет, события быть не должно — но
	// пометка о молчании уже разошлась по потребителям, и снять её может
	// только новая запись в уплотняемом топике.
	store := &хранилищеПамяти{вернулась: true}

	if _, err := служба(store).Accept(t.Context(), годныйСнимок(), nil); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	if len(store.messages) != 2 {
		t.Fatalf("ожидались снимок и состояние, сообщений %d", len(store.messages))
	}
	if store.messages[1].Topic != publish.TopicHosts {
		t.Errorf("состояние ушло в топик %q, ожидался %q", store.messages[1].Topic, publish.TopicHosts)
	}

	var state contract.HostState
	if err := json.Unmarshal(store.messages[1].Payload, &state); err != nil {
		t.Fatalf("состояние не разбирается: %v", err)
	}
	if state.SilentSince != nil {
		t.Error("вернувшаяся машина не должна оставаться помеченной молчащей")
	}
}

func TestИзменениеПорождаетСобытиеИСостояние(t *testing.T) {
	store := &хранилищеПамяти{changes: []contract.Change{
		{Field: "memory.total_bytes", Old: "8 ГБ", New: "16 ГБ"},
	}}

	if _, err := служба(store).Accept(t.Context(), годныйСнимок(), nil); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	if len(store.messages) != 3 {
		t.Fatalf("ожидались снимок, событие и состояние, сообщений %d", len(store.messages))
	}

	var event contract.Event
	if err := json.Unmarshal(store.messages[1].Payload, &event); err != nil {
		t.Fatalf("событие не разбирается: %v", err)
	}
	if event.Type != contract.EventChanged {
		t.Fatalf("вид события %q, ожидался %q", event.Type, contract.EventChanged)
	}
	if len(event.Changes) != 1 || event.Changes[0].New != "16 ГБ" {
		t.Errorf("расхождение не доехало до события: %+v", event.Changes)
	}
	if err := event.Validate(); err != nil {
		t.Errorf("собранное событие не проходит собственную проверку: %v", err)
	}

	if store.messages[2].Topic != publish.TopicHosts {
		t.Errorf("состояние ушло в топик %q, ожидался %q", store.messages[2].Topic, publish.TopicHosts)
	}
}

func TestНеопознаннаяМашинаНеСоздаётЗаписьНоСообщаетОСебе(t *testing.T) {
	// Инвариант I8: запись не создаётся. Но событие нужно — иначе отчёты
	// копятся в очереди, куда никто не смотрит.
	store := &хранилищеПамяти{unresolved: "признаки указывают на разные машины"}

	res, err := служба(store).Accept(t.Context(), годныйСнимок(), nil)
	if err != nil {
		t.Fatalf("неопознанная машина — не ошибка приёма: %v", err)
	}
	if !res.Unresolved {
		t.Fatal("исход должен быть помечен как неопознанный")
	}
	if res.HostID != uuid.Nil {
		t.Error("машины быть не должно")
	}
	if res.Reason == "" {
		t.Error("причина обязана дойти до человека")
	}

	if len(store.messages) != 1 {
		t.Fatalf("ожидалось одно событие, сообщений %d", len(store.messages))
	}

	var event contract.Event
	if err := json.Unmarshal(store.messages[0].Payload, &event); err != nil {
		t.Fatalf("событие не разбирается: %v", err)
	}
	if event.Type != contract.EventUnresolved {
		t.Errorf("вид события %q, ожидался %q", event.Type, contract.EventUnresolved)
	}
	if event.HostID != "" {
		t.Error("у события о неопознанной машине не должно быть идентификатора машины")
	}
	if store.messages[0].Topic != publish.TopicEvents {
		t.Errorf("событие ушло в топик %q", store.messages[0].Topic)
	}
}

func TestПовторНеопознаннойМашиныНеПорождаетСобытия(t *testing.T) {
	// Найдено живым прогоном на седьмом этапе. Машина, которую не удаётся
	// опознать, продолжает слать отчёты — каждые четыре часа. Событие на
	// каждый её приход — это ровно тот шум, из-за которого ленту событий
	// перестают читать (инвариант I9).
	store := &хранилищеПамяти{
		unresolved:       "нет ни одного надёжного признака: только сетевой адрес",
		unresolvedRepeat: true,
	}

	res, err := служба(store).Accept(t.Context(), годныйСнимок(), nil)
	if err != nil {
		t.Fatalf("повтор неопознанной машины — не ошибка приёма: %v", err)
	}
	if !res.Unresolved {
		t.Fatal("исход должен остаться помеченным как неопознанный")
	}
	if len(store.messages) != 0 {
		t.Fatalf("на повторе построено %d сообщений, ожидалось ноль", len(store.messages))
	}
}

func TestСообщениеЛентыНесётСнимокЦеликом(t *testing.T) {
	store := &хранилищеПамяти{}

	if _, err := служба(store).Accept(t.Context(), годныйСнимок(), nil); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	var envelope struct {
		SchemaVersion int               `json:"schema_version"`
		HostID        string            `json:"host_id"`
		Snapshot      contract.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(store.messages[0].Payload, &envelope); err != nil {
		t.Fatalf("сообщение не разбирается: %v", err)
	}

	if envelope.SchemaVersion != contract.SchemaVersion {
		t.Errorf("версия контракта в сообщении: %d", envelope.SchemaVersion)
	}
	if envelope.Snapshot.Machine.Hostname != "PC-214-01" {
		t.Errorf("снимок в сообщении потерян: %+v", envelope.Snapshot.Machine)
	}
}

func TestОшибкаБазыНеПревращаетсяВНегодныйОтчёт(t *testing.T) {
	// Иначе агент получит 400, решит, что сам виноват, и перестанет
	// повторять — а проблема была на стороне службы.
	store := &хранилищеПамяти{err: errors.New("база недоступна")}

	_, err := служба(store).Accept(t.Context(), годныйСнимок(), nil)
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if errors.Is(err, ErrInvalid) {
		t.Error("сбой базы не должен выглядеть как негодный отчёт")
	}
}
