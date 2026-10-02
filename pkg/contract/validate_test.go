package contract

import (
	"strings"
	"testing"
	"time"
)

var сейчас = time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)

func TestГодныйСнимокПроходит(t *testing.T) {
	if err := образецСнимка().Validate(сейчас); err != nil {
		t.Fatalf("образец должен быть годен: %v", err)
	}
}

func TestЧастичныйСборНеДелаетСнимокНегодным(t *testing.T) {
	// Частичный сбор — норма, а не сбой: WMI на старой Windows регулярно
	// отдаёт не всё. Отказ от такого снимка означал бы, что машина с
	// нечитаемым S.M.A.R.T. вообще не попадёт в учёт.
	s := образецСнимка()
	s.Disks = nil
	s.Software = nil
	s.Failures = []Failure{{Section: "disks", Reason: "WMI: доступ запрещён"}}

	if err := s.Validate(сейчас); err != nil {
		t.Fatalf("снимок с пропусками должен приниматься: %v", err)
	}
}

func TestСнимокБезПризнаковМашиныОтвергается(t *testing.T) {
	s := Snapshot{
		SchemaVersion: SchemaVersion,
		Origin:        OriginNetwork,
		CollectedAt:   сейчас,
	}

	err := s.Validate(сейчас)
	if err == nil {
		t.Fatal("ожидался отказ")
	}
	if !strings.Contains(err.Error(), "отметка агента") {
		t.Errorf("ошибка должна назвать причину, получено: %v", err)
	}
}

func TestВремяСбораИзБудущего(t *testing.T) {
	s := образецСнимка()
	s.CollectedAt = сейчас.Add(2 * time.Hour)

	if err := s.Validate(сейчас); err == nil {
		t.Fatal("снимок из будущего должен отвергаться")
	}
}

func TestЧасыМогутНемногоВрать(t *testing.T) {
	// Часы на рабочих станциях уезжают, и жёсткое «не позже сейчас»
	// отбрасывало бы нормальные отчёты.
	s := образецСнимка()
	s.CollectedAt = сейчас.Add(10 * time.Minute)

	if err := s.Validate(сейчас); err != nil {
		t.Fatalf("отклонение часов в пределах допуска должно приниматься: %v", err)
	}
}

func TestОтчётСФлешкиМесячнойДавностиПринимается(t *testing.T) {
	// Машины без сети шлют отчёт файлом, и файл может пролежать недели.
	s := образецСнимка()
	s.Origin = OriginOffline
	s.CollectedAt = сейчас.Add(-30 * 24 * time.Hour)

	if err := s.Validate(сейчас); err != nil {
		t.Fatalf("месячный автономный отчёт должен приниматься: %v", err)
	}
}

func TestОтчётГодовойДавностиОтвергается(t *testing.T) {
	s := образецСнимка()
	s.Origin = OriginOffline
	s.CollectedAt = сейчас.Add(-365 * 24 * time.Hour)

	if err := s.Validate(сейчас); err == nil {
		t.Fatal("снимок годовой давности применять к карточке нельзя")
	}
}

func TestНеизвестныйСпособДоставки(t *testing.T) {
	s := образецСнимка()
	s.Origin = "почтовым голубем"

	if err := s.Validate(сейчас); err == nil {
		t.Fatal("ожидался отказ")
	}
}

func TestВерсияКонтрактаНовееПоддерживаемой(t *testing.T) {
	s := образецСнимка()
	s.SchemaVersion = SchemaVersion + 5

	if err := s.Validate(сейчас); err == nil {
		t.Fatal("версию, которой ещё не существует, принимать нельзя")
	}
}

func TestЗаглушкиПроизводителяНеСчитаютсяСерийнымНомером(t *testing.T) {
	// Опознание по такой «строке» склеило бы в одну машину весь сборный парк.
	заглушки := []string{
		"To Be Filled By O.E.M.",
		"  default string  ",
		"System Serial Number",
		"None",
		"0",
		"",
		"AB",
	}
	for _, s := range заглушки {
		if got := UsableSerial(s); got != "" {
			t.Errorf("UsableSerial(%q) = %q, ожидалась пустая строка", s, got)
		}
	}

	if got := UsableSerial(" 7XKPL03 "); got != "7XKPL03" {
		t.Errorf("настоящий номер должен сохраниться и обрезаться по краям, получено %q", got)
	}
}

func TestСобытиеОбИзмененииБезИзмененийОтвергается(t *testing.T) {
	// Иначе инвариант I9 нарушается тихо: событие «изменилось» без единого
	// изменения — это и есть тот шум, ради борьбы с которым он заведён.
	e := Event{
		SchemaVersion: SchemaVersion,
		Type:          EventChanged,
		HostID:        "host-1",
		OccurredAt:    сейчас,
	}

	if err := e.Validate(); err == nil {
		t.Fatal("ожидался отказ")
	}
}

func TestСобытиюОНеопознаннойМашинеHostIDНеНужен(t *testing.T) {
	// Машины ещё нет: запись не создаётся до разбора человеком (I8).
	e := Event{
		SchemaVersion: SchemaVersion,
		Type:          EventUnresolved,
		OccurredAt:    сейчас,
		Reason:        "ни один признак не совпал",
	}

	if err := e.Validate(); err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
}

func TestОстальнымСобытиямHostIDОбязателен(t *testing.T) {
	for _, тип := range []EventType{EventFirstSeen, EventChanged, EventSilent} {
		e := Event{SchemaVersion: SchemaVersion, Type: тип, OccurredAt: сейчас}
		if err := e.Validate(); err == nil {
			t.Errorf("событие %s без host_id должно отвергаться", тип)
		}
	}
}
