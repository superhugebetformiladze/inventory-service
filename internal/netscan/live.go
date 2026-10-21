package netscan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// SNMPOptions — параметры живого опроса.
type SNMPOptions struct {
	// Community — строка доступа SNMPv2c только на чтение. Секрет (I10):
	// приходит из окружения, значения по умолчанию нет.
	Community string
	Port      uint16
	Timeout   time.Duration
	Retries   int
	// MaxRepetitions — сколько строк просить за один GETBULK. Таблица FDB
	// коммутатора доступа — сотни строк; 25 — разумный компромисс между
	// числом запросов и размером ответа, который не развалится на UDP.
	MaxRepetitions uint32
}

// LiveWalker — опрос настоящего устройства по SNMPv2c.
type LiveWalker struct {
	target string
	opts   SNMPOptions
}

// NewLiveWalker готовит опрос. Соединение открывается на каждый обход: UDP
// дёшев, а держать сокет между обходами значит держать его и тогда, когда
// устройство уже перезагрузили.
func NewLiveWalker(target string, opts SNMPOptions) (*LiveWalker, error) {
	// Пробелы и перевод строки по краям — не часть строки доступа, а след
	// редактора: .env, сохранённый в Windows, отдаёт значение с «\r» на конце,
	// и коммутатор молча не отвечает на чужую строку (найдено на живой сети).
	opts.Community = strings.TrimSpace(opts.Community)
	if opts.Community == "" {
		return nil, fmt.Errorf("не задана строка доступа SNMP")
	}
	if opts.Port == 0 {
		opts.Port = 161
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.Retries < 0 {
		opts.Retries = 0
	}
	if opts.MaxRepetitions == 0 {
		opts.MaxRepetitions = 25
	}
	return &LiveWalker{target: target, opts: opts}, nil
}

// Walk обходит поддерево GETBULK-запросами.
//
// Контекст соблюдается на каждой переменной (I11): у gosnmp свой таймаут на
// запрос, но обход большой таблицы — это много запросов, и общий срок задаёт
// вызывающий.
func (w *LiveWalker) Walk(ctx context.Context, root string, fn func(Variable) error) error {
	client := &gosnmp.GoSNMP{
		Target:         w.target,
		Port:           w.opts.Port,
		Community:      w.opts.Community,
		Version:        gosnmp.Version2c,
		Timeout:        w.opts.Timeout,
		Retries:        w.opts.Retries,
		MaxRepetitions: w.opts.MaxRepetitions,
		Context:        ctx,
	}
	if err := client.Connect(); err != nil {
		return fmt.Errorf("подключение к %s: %w", w.target, err)
	}
	defer func() { _ = client.Conn.Close() }()

	return client.BulkWalk("."+root, func(pdu gosnmp.SnmpPDU) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		v := fromPDU(pdu)
		if v.Kind == KindNull {
			return nil
		}
		return fn(v)
	})
}

func fromPDU(pdu gosnmp.SnmpPDU) Variable {
	v := Variable{OID: strings.TrimPrefix(pdu.Name, ".")}

	switch pdu.Type {
	case gosnmp.OctetString:
		b, _ := pdu.Value.([]byte)
		v.Kind, v.Bytes = KindBytes, b
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks,
		gosnmp.Counter64, gosnmp.Uinteger32:
		v.Kind, v.Int = KindInt, gosnmp.ToBigInt(pdu.Value).Int64()
	case gosnmp.ObjectIdentifier:
		s, _ := pdu.Value.(string)
		v.Kind, v.Text = KindOID, strings.TrimPrefix(s, ".")
	case gosnmp.IPAddress:
		s, _ := pdu.Value.(string)
		v.Kind, v.Text = KindIP, s
	default:
		v.Kind = KindNull
	}
	return v
}
