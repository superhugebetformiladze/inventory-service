package contract

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxClockSkew — насколько снимок может быть «из будущего», прежде чем его
// сочтут негодным.
//
// Часы на рабочих станциях врут, и жёсткое «не позже сейчас» отбрасывало бы
// нормальные отчёты машин с уехавшим временем. Полчаса — запас, за которым
// уже не сбой часов, а подделка даты.
const MaxClockSkew = 30 * time.Minute

// MaxSnapshotAge — насколько старым может быть снимок.
//
// Машины без сети шлют отчёты файлом, и такой файл может пролежать на флешке
// неделями. Но год — это уже не отчёт, а археология: применять его к карточке
// поверх свежих данных нельзя.
const MaxSnapshotAge = 180 * 24 * time.Hour

// ErrEmptySnapshot возвращается, когда в снимке нет ничего, что позволило бы
// его отнести к машине.
var ErrEmptySnapshot = errors.New("снимок не содержит ни одного признака машины")

// Validate проверяет снимок на пригодность к обработке.
//
// Проверяется только то, без чего дальше нельзя: версия, отметка агента,
// вменяемое время и наличие хотя бы одного признака для опознания. Полнота
// сбора НЕ проверяется — частичный снимок это норма (см. Snapshot.Failures),
// и отказ от него означал бы, что машина с нечитаемым S.M.A.R.T. вообще не
// попадёт в учёт.
func (s Snapshot) Validate(now time.Time) error {
	var problems []string

	if s.SchemaVersion <= 0 {
		problems = append(problems, "не указана версия контракта")
	}
	if s.SchemaVersion > SchemaVersion {
		// Не ошибка, а предупреждение по смыслу: читатель обязан
		// игнорировать незнакомые поля (I5). Но версию, которой ещё не
		// существует, служба принять не может — она не знает, что в ней
		// поменялось несовместимо.
		problems = append(problems, fmt.Sprintf(
			"версия контракта %d новее поддерживаемой %d", s.SchemaVersion, SchemaVersion))
	}

	if strings.TrimSpace(s.AgentID) == "" {
		problems = append(problems, "пустая отметка агента")
	}

	switch s.Origin {
	case OriginNetwork, OriginOffline:
	case "":
		problems = append(problems, "не указан способ доставки отчёта")
	default:
		problems = append(problems, fmt.Sprintf("неизвестный способ доставки %q", s.Origin))
	}

	switch {
	case s.CollectedAt.IsZero():
		problems = append(problems, "не указано время сбора")
	case s.CollectedAt.After(now.Add(MaxClockSkew)):
		problems = append(problems, "время сбора в будущем")
	case now.Sub(s.CollectedAt) > MaxSnapshotAge:
		problems = append(problems, "снимок старше полугода")
	}

	if !s.hasAnyIdentifier() {
		problems = append(problems, ErrEmptySnapshot.Error())
	}

	if len(problems) > 0 {
		// Без приставки вроде «снимок негоден»: вызывающий добавит свою, и
		// агент получил бы «отчёт негоден: снимок негоден: ...». Перечень
		// причин — это всё, что здесь есть сказать.
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// hasAnyIdentifier — есть ли хоть что-то, по чему машину можно опознать.
func (s Snapshot) hasAnyIdentifier() bool {
	if strings.TrimSpace(s.AgentID) != "" {
		return true
	}
	if UsableSerial(s.Machine.Serial) != "" {
		return true
	}
	if strings.TrimSpace(s.Machine.Hostname) != "" {
		return true
	}
	return len(s.PhysicalInterfaces()) > 0
}

// placeholderSerials — то, что производители пишут в поле серийного номера
// вместо номера. Формально строка непустая, фактически признака нет, и
// опознание по ней склеило бы в одну машину весь сборный парк.
var placeholderSerials = []string{
	"to be filled by o.e.m.",
	"to be filled by oem",
	"system serial number",
	"default string",
	"none",
	"n/a",
	"na",
	"unknown",
	"0",
	"00000000",
	"123456789",
	"................",
}

// UsableSerial возвращает серийный номер, если он годится для опознания, и
// пустую строку, если это заглушка производителя.
func UsableSerial(serial string) string {
	trimmed := strings.TrimSpace(serial)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	for _, p := range placeholderSerials {
		if lower == p {
			return ""
		}
	}

	// Односимвольный «номер» признаком тоже не является.
	if len([]rune(trimmed)) < 3 {
		return ""
	}

	return trimmed
}

// Validate проверяет событие.
func (e Event) Validate() error {
	var problems []string

	if e.SchemaVersion <= 0 {
		problems = append(problems, "не указана версия контракта")
	}

	switch e.Type {
	case EventFirstSeen, EventChanged, EventSilent:
		if e.HostID == "" {
			problems = append(problems, fmt.Sprintf("событию %s нужен host_id", e.Type))
		}
	case EventUnresolved:
		// host_id пуст намеренно: машины ещё нет (I8).
	case "":
		problems = append(problems, "не указан вид события")
	default:
		problems = append(problems, fmt.Sprintf("неизвестный вид события %q", e.Type))
	}

	if e.Type == EventChanged && len(e.Changes) == 0 {
		// Иначе инвариант I9 нарушается тихо: событие «изменилось» без
		// единого изменения — это и есть тот шум, ради борьбы с которым
		// инвариант заведён.
		problems = append(problems, "событие об изменении без единого изменения")
	}

	if e.OccurredAt.IsZero() {
		problems = append(problems, "не указано время события")
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
