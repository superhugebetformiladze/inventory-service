package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

var сейчас = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// хранилищеПамяти изображает очередь заданий в базе, включая аренду.
type хранилищеПамяти struct {
	задания []*storage.CommandRow
	err     error
}

func (х *хранилищеПамяти) CreateCommand(
	_ context.Context, agentUUID uuid.UUID, hostID *uuid.UUID,
	kind contract.CommandKind, params map[string]any,
	createdBy string, expiresAt time.Time,
) (uuid.UUID, error) {
	if х.err != nil {
		return uuid.Nil, х.err
	}
	row := &storage.CommandRow{
		ID: uuid.New(), AgentUUID: agentUUID, HostID: hostID,
		Kind: kind, Params: params, CreatedBy: createdBy,
		CreatedAt: сейчас, ExpiresAt: expiresAt,
	}
	х.задания = append(х.задания, row)
	return row.ID, nil
}

func (х *хранилищеПамяти) TakeCommands(
	_ context.Context, agentUUID uuid.UUID, now time.Time, limit int,
) ([]storage.CommandRow, error) {
	var out []storage.CommandRow
	for _, c := range х.задания {
		if len(out) >= limit {
			break
		}
		// То же условие, что в SQL: не выполнено, не просрочено, и либо ещё
		// никому не выдавалось, либо аренда истекла.
		арендаИстекла := c.TakenAt != nil && c.TakenAt.Before(now.Add(-storage.CommandLease))
		switch {
		case c.AgentUUID != agentUUID, c.DoneAt != nil, !c.ExpiresAt.After(now):
			continue
		case c.TakenAt != nil && !арендаИстекла:
			continue
		}
		c.TakenAt = &now
		c.Attempts++
		out = append(out, *c)
	}
	return out, nil
}

func (х *хранилищеПамяти) FinishCommand(
	_ context.Context, id, agentUUID uuid.UUID, ok bool, errText string, now time.Time,
) error {
	for _, c := range х.задания {
		if c.ID != id || c.AgentUUID != agentUUID || c.DoneAt != nil {
			continue
		}
		c.DoneAt = &now
		c.OK = &ok
		c.Error = errText
		return nil
	}
	return storage.ErrNotFound
}

func (х *хранилищеПамяти) AgentOfHost(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, storage.ErrNotFound
}

func (х *хранилищеПамяти) Facts(_ context.Context, _ uuid.UUID) (storage.HostFacts, error) {
	return storage.HostFacts{}, storage.ErrNotFound
}

func служба(х *хранилищеПамяти) *Service {
	return New(х, 24*time.Hour, func() time.Time { return сейчас })
}

func TestЗаданиеВыдаётсяОдинРаз(t *testing.T) {
	х := &хранилищеПамяти{}
	s := служба(х)
	агент := uuid.New()

	if _, err := s.Create(t.Context(), агент, nil, contract.CommandCollectNow, nil, "человек"); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	первый, err := s.Take(t.Context(), агент, 10)
	if err != nil || len(первый) != 1 {
		t.Fatalf("первый опрос: %d заданий, ошибка %v", len(первый), err)
	}

	второй, err := s.Take(t.Context(), агент, 10)
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if len(второй) != 0 {
		t.Fatalf("задание выдано повторно: %d", len(второй))
	}
}

func TestПотерянноеЗаданиеВозвращаетсяВОчередь(t *testing.T) {
	// Агент упал, потерял сеть или был выключен сразу после получения. Без
	// возврата одно падение агента означало бы навсегда потерянное «собрать
	// сейчас», и человек ждал бы ответа, которого не будет.
	х := &хранилищеПамяти{}
	агент := uuid.New()

	s := служба(х)
	if _, err := s.Create(t.Context(), агент, nil, contract.CommandCollectNow, nil, "человек"); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if got, _ := s.Take(t.Context(), агент, 10); len(got) != 1 {
		t.Fatalf("первый опрос выдал %d заданий", len(got))
	}

	// Прошла аренда, агент так и не отчитался.
	позже := New(х, 24*time.Hour, func() time.Time {
		return сейчас.Add(storage.CommandLease + time.Minute)
	})

	вернулось, err := позже.Take(t.Context(), агент, 10)
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if len(вернулось) != 1 {
		t.Fatalf("задание не вернулось в очередь: %d", len(вернулось))
	}
	if вернулось[0].ID == "" {
		t.Error("у вернувшегося задания нет идентификатора")
	}
}

func TestВыполненноеЗаданиеБольшеНеВыдаётся(t *testing.T) {
	х := &хранилищеПамяти{}
	s := служба(х)
	агент := uuid.New()

	id, _ := s.Create(t.Context(), агент, nil, contract.CommandCollectNow, nil, "человек")
	выданные, _ := s.Take(t.Context(), агент, 10)
	if len(выданные) != 1 {
		t.Fatalf("выдано %d заданий", len(выданные))
	}

	if err := s.Finish(t.Context(), id, агент, contract.CommandResult{
		SchemaVersion: contract.SchemaVersion, OK: true,
	}); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	позже := New(х, 24*time.Hour, func() time.Time {
		return сейчас.Add(2 * storage.CommandLease)
	})
	if got, _ := позже.Take(t.Context(), агент, 10); len(got) != 0 {
		t.Fatalf("выполненное задание выдано снова: %d", len(got))
	}
}

func TestЧужоеЗаданиеЗакрытьНельзя(t *testing.T) {
	// Иначе агент с действующим ключом закрыл бы чужое задание и оставил
	// соседа ждать вечно.
	х := &хранилищеПамяти{}
	s := служба(х)
	свой, чужой := uuid.New(), uuid.New()

	id, _ := s.Create(t.Context(), свой, nil, contract.CommandCollectNow, nil, "человек")

	err := s.Finish(t.Context(), id, чужой, contract.CommandResult{
		SchemaVersion: contract.SchemaVersion, OK: true,
	})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("ожидался отказ, получено: %v", err)
	}
}

func TestПросроченноеЗаданиеНеВыдаётся(t *testing.T) {
	// «Собрать сейчас», поставленное в понедельник ноутбуку, уехавшему в
	// отпуск, не должно срабатывать через три недели.
	х := &хранилищеПамяти{}
	агент := uuid.New()

	s := New(х, time.Hour, func() time.Time { return сейчас })
	if _, err := s.Create(t.Context(), агент, nil, contract.CommandCollectNow, nil, "человек"); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	позже := New(х, time.Hour, func() time.Time { return сейчас.Add(2 * time.Hour) })
	if got, _ := позже.Take(t.Context(), агент, 10); len(got) != 0 {
		t.Fatalf("просроченное задание выдано: %d", len(got))
	}
}

func TestНеизвестныйВидЗаданияНеСтавится(t *testing.T) {
	// Инвариант I6: список видов закрыт. Иначе очередь заданий превращается в
	// канал произвольных команд на пятьсот машин.
	х := &хранилищеПамяти{}

	_, err := служба(х).Create(t.Context(), uuid.New(), nil,
		contract.CommandKind("rm -rf /"), nil, "злоумышленник")

	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("ожидался отказ, получено: %v", err)
	}
	if len(х.задания) != 0 {
		t.Error("неизвестный вид задания попал в очередь")
	}
}

func TestПериодСбораПроверяетсяНаПределы(t *testing.T) {
	// Пятьсот машин, собирающих сведения каждую секунду, положат и себя, и
	// службу.
	х := &хранилищеПамяти{}
	s := служба(х)

	слишкомЧасто := map[string]any{"interval_seconds": 1}
	if _, err := s.Create(t.Context(), uuid.New(), nil, contract.CommandSetInterval, слишкомЧасто, "человек"); err == nil {
		t.Error("период в секунду должен отвергаться")
	}

	слишкомРедко := map[string]any{"interval_seconds": 60 * 60 * 24 * 30}
	if _, err := s.Create(t.Context(), uuid.New(), nil, contract.CommandSetInterval, слишкомРедко, "человек"); err == nil {
		t.Error("период в месяц должен отвергаться")
	}

	разумно := map[string]any{"interval_seconds": 4 * 60 * 60}
	if _, err := s.Create(t.Context(), uuid.New(), nil, contract.CommandSetInterval, разумно, "человек"); err != nil {
		t.Errorf("разумный период должен приниматься: %v", err)
	}
}

func TestSetIntervalБезПараметраОтвергается(t *testing.T) {
	х := &хранилищеПамяти{}

	if _, err := служба(х).Create(t.Context(), uuid.New(), nil,
		contract.CommandSetInterval, nil, "человек"); err == nil {
		t.Fatal("задание без обязательного параметра должно отвергаться")
	}
}

func TestДлинноеОписаниеОшибкиОбрезается(t *testing.T) {
	// Агент может прислать стектрейс целиком. В очереди заданий его никто не
	// читает, а место он занимает.
	х := &хранилищеПамяти{}
	s := служба(х)
	агент := uuid.New()

	id, _ := s.Create(t.Context(), агент, nil, contract.CommandCollectNow, nil, "человек")
	_, _ = s.Take(t.Context(), агент, 10)

	if err := s.Finish(t.Context(), id, агент, contract.CommandResult{
		SchemaVersion: contract.SchemaVersion,
		OK:            false,
		Error:         strings.Repeat("стектрейс ", 500),
	}); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	if len([]rune(х.задания[0].Error)) > 520 {
		t.Errorf("описание ошибки не обрезано: %d символов", len([]rune(х.задания[0].Error)))
	}
}

func TestВидыЗаданийЗакрытыйСписок(t *testing.T) {
	for _, kind := range contract.KnownCommandKinds {
		if !kind.Valid() {
			t.Errorf("вид %q объявлен известным, но не проходит проверку", kind)
		}
	}
	for _, чужое := range []contract.CommandKind{"", "shell", "update", "COLLECT_NOW"} {
		if чужое.Valid() {
			t.Errorf("вид %q не должен признаваться", чужое)
		}
	}
}
