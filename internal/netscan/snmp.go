// Package netscan — сбор сведений об управляемых коммутаторах по SNMP.
//
// Опрос только на чтение: таблицы портов (IF-MIB), выученные MAC-адреса за
// портами (BRIDGE-MIB и Q-BRIDGE-MIB), соседи по LLDP и ARP-таблица ядра.
// Из этого потребитель строит, какое устройство в какой порт включено и как
// коммутаторы соединены между собой. Выводов здесь нет — только факты,
// которые сообщило устройство (инвариант I1).
//
// Источник данных спрятан за интерфейсом Walker. Живой опрос — одна его
// реализация, файл `snmpwalk` — другая: службе, у которой ещё нет сетевого
// доступа к коммутаторам, снятые на другой машине обходы скармливаются той же
// логике, по которой потом пойдёт живой опрос.
package netscan

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Kind — вид значения переменной SNMP.
type Kind int

const (
	// KindBytes — строка октетов: имена, описания, MAC-адреса.
	KindBytes Kind = iota
	// KindInt — любое целое: INTEGER, Gauge32, Counter, TimeTicks.
	KindInt
	// KindOID — идентификатор объекта, например sysObjectID.
	KindOID
	// KindIP — IpAddress.
	KindIP
	// KindNull — значения нет: noSuchObject, noSuchInstance, endOfMibView.
	KindNull
)

// Variable — одна переменная обхода.
type Variable struct {
	// OID без ведущей точки: "1.3.6.1.2.1.1.5.0".
	OID   string
	Kind  Kind
	Bytes []byte
	Int   int64
	// Text — значение KindOID и KindIP.
	Text string
}

// String — строка октетов как текст. Нулевые байты в конце отбрасываются:
// часть устройств дополняет ими имена до фиксированной длины.
func (v Variable) String() string {
	switch v.Kind {
	case KindBytes:
		s := strings.TrimRight(string(v.Bytes), "\x00")
		if !utf8.ValidString(s) {
			return strings.ToValidUTF8(s, "?")
		}
		return strings.TrimSpace(s)
	case KindInt:
		return strconv.FormatInt(v.Int, 10)
	default:
		return v.Text
	}
}

// Walker обходит поддерево MIB.
//
// fn вызывается для каждой переменной поддерева по порядку. Отсутствующее у
// устройства поддерево — не ошибка: обход просто ничего не вызывает. Ошибка —
// когда устройство не ответило.
type Walker interface {
	Walk(ctx context.Context, root string, fn func(Variable) error) error
}

// Корни обходов. Каждый — отдельный обход: так проще понять по журналу, на
// чём устройство перестало отвечать, и не тянуть всю MIB целиком.
const (
	oidSystem = "1.3.6.1.2.1.1"

	oidIfDescr  = "1.3.6.1.2.1.2.2.1.2"
	oidIfType   = "1.3.6.1.2.1.2.2.1.3"
	oidIfAdmin  = "1.3.6.1.2.1.2.2.1.7"
	oidIfOper   = "1.3.6.1.2.1.2.2.1.8"
	oidIfName   = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfSpeedH = "1.3.6.1.2.1.31.1.1.1.15"
	oidIfAlias  = "1.3.6.1.2.1.31.1.1.1.18"

	oidBridgeAddress  = "1.3.6.1.2.1.17.1.1"
	oidBasePortIfIdx  = "1.3.6.1.2.1.17.1.4.1.2"
	oidDot1dFdbPort   = "1.3.6.1.2.1.17.4.3.1.2"
	oidDot1dFdbStatus = "1.3.6.1.2.1.17.4.3.1.3"
	oidDot1qFdbPort   = "1.3.6.1.2.1.17.7.1.2.2.1.2"
	oidDot1qFdbStatus = "1.3.6.1.2.1.17.7.1.2.2.1.3"
	oidDot1qPvid      = "1.3.6.1.2.1.17.7.1.4.5.1.1"

	oidLldpLocChassisSubtype = "1.0.8802.1.1.2.1.3.1"
	oidLldpLocChassisID      = "1.0.8802.1.1.2.1.3.2"
	oidLldpRemTable          = "1.0.8802.1.1.2.1.4.1.1"

	oidArpPhys = "1.3.6.1.2.1.4.22.1.2"
	oidArpType = "1.3.6.1.2.1.4.22.1.4"
)

// Roots — всё, что обходит сбор. Нужен и сценарию снятия обходов вручную:
// обойти ровно то, что потом прочтёт сбор, и не больше.
var Roots = []string{
	oidSystem,
	"1.3.6.1.2.1.2.2.1",
	"1.3.6.1.2.1.31.1.1.1",
	"1.3.6.1.2.1.17",
	"1.0.8802.1.1.2.1",
	"1.3.6.1.2.1.4.22.1",
}

// suffix — хвост OID после корня, или "" и false, если OID не из поддерева.
func suffix(oid, root string) (string, bool) {
	if !strings.HasPrefix(oid, root+".") {
		return "", false
	}
	return oid[len(root)+1:], true
}

// parseIndex разбирает хвост OID в числа.
func parseIndex(s string) ([]int, bool) {
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}
