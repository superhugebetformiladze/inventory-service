package contract

import "time"

// EventType — вид события в ленте.
type EventType string

const (
	// EventFirstSeen — машина опознана впервые и заведена в службе.
	EventFirstSeen EventType = "host.first_seen"

	// EventChanged — состав или сеть машины изменились. Публикуется только
	// при фактическом расхождении со свежим снимком (инвариант I9): иначе
	// девятнадцать отчётов из двадцати породили бы событие «ничего не
	// изменилось», и настоящее изменение утонуло бы в этом потоке.
	EventChanged EventType = "host.changed"

	// EventSilent — машина не выходила на связь дольше порога. Событие
	// порождает не отчёт, а его отсутствие, поэтому publisher у него —
	// периодическая задача службы, а не приём.
	EventSilent EventType = "host.silent"

	// EventUnresolved — пришёл отчёт от машины, которую опознать не удалось.
	// Запись при этом НЕ создаётся (инвариант I8): отчёт уходит в очередь
	// разбора, а событие сообщает потребителю, что там появилась работа.
	EventUnresolved EventType = "host.unresolved"
)

// Event — сообщение в топике inventory.events.v1.
//
// Ключ сообщения — HostID (инвариант I4): все события одной машины идут по
// порядку, разные машины обрабатываются параллельно.
type Event struct {
	SchemaVersion int       `json:"schema_version"`
	Type          EventType `json:"type"`

	// HostID — идентификатор машины в службе. У EventUnresolved пуст: машины
	// ещё нет.
	HostID string `json:"host_id,omitempty"`

	OccurredAt time.Time `json:"occurred_at"`

	// SnapshotID — снимок, из-за которого событие возникло. Позволяет
	// потребителю запросить подробности и отбросить повтор (инвариант I3).
	SnapshotID string `json:"snapshot_id,omitempty"`

	// Changes заполняется только у EventChanged.
	Changes []Change `json:"changes,omitempty"`

	// Reason заполняется у EventUnresolved и EventSilent.
	Reason string `json:"reason,omitempty"`
}

// Change — одно расхождение между снимками.
//
// Значения строками намеренно: потребитель показывает их человеку («было 8 ГБ,
// стало 16 ГБ»), а не считает по ним. Для счёта есть сам снимок.
type Change struct {
	// Field — путь в снимке: memory.total_bytes, disks, interfaces[0].ipv4.
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// HostState — сообщение в уплотняемом топике inventory.hosts.v1: актуальное
// состояние машины на данный момент.
//
// Смысл отдельного топика в том, что новый потребитель читает его целиком и
// строит картину всего парка с нуля, не разбирая тридцатидневную историю
// снимков и не спрашивая службу ни о чём.
type HostState struct {
	SchemaVersion int    `json:"schema_version"`
	HostID        string `json:"host_id"`

	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastReportAt time.Time `json:"last_report_at"`

	// Snapshot — последний снимок целиком.
	Snapshot Snapshot `json:"snapshot"`

	// SilentSince — с какого момента машина не выходит на связь. Пусто —
	// значит выходит.
	//
	// Здесь, а не только событием host.silent: событие уходит один раз, и
	// потребитель, поднятый через месяц, его уже не увидит. Уплотняемый топик
	// на то и уплотняемый, чтобы отвечать на вопрос «как обстоят дела
	// сейчас» тому, кто читает его с начала.
	SilentSince *time.Time `json:"silent_since,omitempty"`

	// Retired — машина выведена из учёта. В уплотняемом топике запись нельзя
	// просто удалить: потребитель, читающий с начала, никогда не узнал бы об
	// этом. Поэтому выбытие — тоже сообщение.
	Retired bool `json:"retired,omitempty"`
}
