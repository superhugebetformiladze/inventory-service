package contract

import (
	"errors"
	"fmt"
	"time"
)

// CommandKind — что можно поручить агенту.
//
// Это ЗАКРЫТЫЙ список (инвариант I6), и он лежит в контракте, а не в службе,
// именно поэтому: агент сверяет полученное с этим перечислением и отказывается
// от всего остального. Не «сервер прислал строку — выполним», а «сервер может
// попросить одно из двух».
//
// Канал, по которому серверу можно послать любой машине любую команду, — это
// средство удалённого управления всем парком компании. Однажды им
// воспользуется не тот человек, и цена ошибки будет несопоставима с пользой от
// инвентаризации. Поэтому список расширяется только осознанно, обеими
// сторонами сразу, и каждое добавление — это решение, а не удобство.
type CommandKind string

const (
	// CommandCollectNow — собрать сведения и прислать отчёт немедленно, не
	// дожидаясь своего расписания. То, что стоит за кнопкой «Обновить
	// сейчас» в карточке компьютера.
	CommandCollectNow CommandKind = "collect_now"

	// CommandSetInterval — изменить период сбора. Параметр interval_seconds.
	CommandSetInterval CommandKind = "set_interval"
)

// KnownCommandKinds — весь список целиком. Используется и службой при
// создании задания, и агентом при получении.
var KnownCommandKinds = []CommandKind{CommandCollectNow, CommandSetInterval}

// Valid — знаком ли вид задания.
func (k CommandKind) Valid() bool {
	for _, known := range KnownCommandKinds {
		if k == known {
			return true
		}
	}
	return false
}

// Пределы периода сбора.
//
// Нижний — чтобы ошибка в цифре не превратила парк в источник постоянной
// нагрузки: пятьсот машин, собирающих сведения каждую секунду, положат и себя,
// и службу. Верхний — чтобы «раз в год» не выглядело работающей настройкой.
const (
	MinCollectInterval = 5 * time.Minute
	MaxCollectInterval = 7 * 24 * time.Hour
)

// Command — задание агенту.
type Command struct {
	SchemaVersion int         `json:"schema_version"`
	ID            string      `json:"id"`
	Kind          CommandKind `json:"kind"`

	// Params — параметры вида задания. У collect_now пусты.
	Params map[string]any `json:"params,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// IntervalSeconds достаёт период сбора из параметров.
func (c Command) IntervalSeconds() (int, bool) {
	raw, ok := c.Params["interval_seconds"]
	if !ok {
		return 0, false
	}

	// JSON приносит числа как float64; сюда же может прийти int, если
	// структуру собрали в коде, а не разобрали из ленты.
	switch v := raw.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

// Validate проверяет задание перед выполнением.
//
// Проверяет ОБЕ стороны: служба — прежде чем поставить в очередь, агент —
// прежде чем выполнить. Вторая проверка не лишняя: агент не должен доверять
// тому, что служба прислала правильное, — иначе взломанная служба становится
// пультом управления парком.
func (c Command) Validate() error {
	if c.SchemaVersion <= 0 {
		return errors.New("не указана версия контракта")
	}
	if !c.Kind.Valid() {
		return fmt.Errorf("неизвестный вид задания %q", c.Kind)
	}

	if c.Kind == CommandSetInterval {
		seconds, ok := c.IntervalSeconds()
		if !ok {
			return errors.New("для set_interval нужен параметр interval_seconds")
		}
		interval := time.Duration(seconds) * time.Second
		if interval < MinCollectInterval || interval > MaxCollectInterval {
			return fmt.Errorf("период сбора %s вне допустимого (%s…%s)",
				interval, MinCollectInterval, MaxCollectInterval)
		}
	}

	return nil
}

// CommandResult — что агент сообщает о выполнении.
type CommandResult struct {
	SchemaVersion int    `json:"schema_version"`
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
}
