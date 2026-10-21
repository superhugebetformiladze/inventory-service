package netscan

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ExecWalker — опрос программой snmpbulkwalk из net-snmp (SNMPv2c).
//
// Вторая реализация рядом с LiveWalker, а не замена ему. net-snmp — то, чем
// администраторы сети проверяют коммутаторы руками, и когда опрос «не
// работает», первый вопрос — «а snmpwalk отвечает?». С этим walker'ом служба
// делает ровно то же, что человек в консоли, и спор «библиотека или сеть»
// не возникает.
//
// Вывод разбирается тем же ParseWalk, что и файлы обходов, поэтому формат
// фиксирован: -On (числовые OID), -Ox (октеты байтами), и без MIB-файлов
// (-m ""). С MIB-файлами net-snmp печатает PhysAddress по подсказке
// отображения — «STRING: 2:42:ac:11:0:2», — и MAC в ARP-таблице приходит
// текстом вместо байтов (проверено на net-snmp 5.9.4).
//
// Строка доступа не передаётся в командной строке: аргументы процесса видны
// всем в системе (ps, /proc). Она лежит в snmp.conf во временном каталоге с
// правами только владельца, а программа находит его по SNMPCONFPATH.
type ExecWalker struct {
	target  string
	opts    SNMPOptions
	confDir string
	program string
}

// SNMPProgram — программа обхода по умолчанию.
const SNMPProgram = "snmpbulkwalk"

// WriteSNMPConf кладёт строку доступа в snmp.conf во временный каталог.
//
// Возвращает каталог; удалить его — забота вызывающего.
func WriteSNMPConf(community string) (string, error) {
	community = strings.TrimSpace(community)
	if community == "" {
		return "", fmt.Errorf("не задана строка доступа SNMP")
	}
	if strings.ContainsAny(community, "\n\r") {
		return "", fmt.Errorf("строка доступа SNMP не может содержать перевод строки")
	}
	dir, err := os.MkdirTemp("", "netscan-snmp-")
	if err != nil {
		return "", fmt.Errorf("каталог настроек SNMP: %w", err)
	}
	body := "defVersion 2c\ndefCommunity " + community + "\n"
	if err := os.WriteFile(filepath.Join(dir, "snmp.conf"), []byte(body), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("файл настроек SNMP: %w", err)
	}
	return dir, nil
}

// NewExecWalker готовит опрос программой. confDir — из WriteSNMPConf.
func NewExecWalker(target string, opts SNMPOptions, confDir string) (*ExecWalker, error) {
	if confDir == "" {
		return nil, fmt.Errorf("не задан каталог настроек SNMP")
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
	return &ExecWalker{target: target, opts: opts, confDir: confDir, program: SNMPProgram}, nil
}

// args — аргументы одного обхода.
func (w *ExecWalker) args(root string) []string {
	seconds := int(math.Ceil(w.opts.Timeout.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return []string{
		"-v2c",
		"-On", "-Ox",
		"-m", "", // без MIB-файлов: см. комментарий к типу
		"-t", strconv.Itoa(seconds),
		"-r", strconv.Itoa(w.opts.Retries),
		"-Cr" + strconv.FormatUint(uint64(w.opts.MaxRepetitions), 10),
		fmt.Sprintf("udp:%s:%d", w.target, w.opts.Port),
		"." + root,
	}
}

// Walk обходит поддерево одним запуском программы.
//
// Контекст прерывает процесс (I11): сам snmpbulkwalk ждёт ответа сколько
// сказано в -t на каждый запрос, а таблица — это много запросов.
func (w *ExecWalker) Walk(ctx context.Context, root string, fn func(Variable) error) error {
	cmd := exec.CommandContext(ctx, w.program, w.args(root)...) //nolint:gosec // программа своя, аргументы — из настроек
	// Только то, что программе нужно: путь и где взять настройки. Каталог
	// постоянных данных — туда же, иначе net-snmp пытается писать в домашний.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SNMPCONFPATH=" + w.confDir,
		"SNMP_PERSISTENT_DIR=" + w.confDir,
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// «Timeout: No Response from …» — самое частое и самое понятное, что
		// программа может сказать; оно и уходит в статус адреса.
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s %s: %s", w.program, w.target, firstLine(msg))
		}
		return fmt.Errorf("%s %s: %w", w.program, w.target, err)
	}

	parsed, err := ParseWalk(&stdout)
	if err != nil {
		return fmt.Errorf("%s %s: %w", w.program, w.target, err)
	}
	return parsed.Walk(ctx, root, fn)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// Способы опроса — значение NETSCAN_WALKER.
const (
	// WalkerSnmpwalk — программой snmpbulkwalk (по умолчанию).
	WalkerSnmpwalk = "snmpwalk"
	// WalkerGosnmp — встроенной библиотекой, без внешних программ.
	WalkerGosnmp = "gosnmp"
)

// NewDialer готовит обход адресов выбранным способом.
//
// cleanup убирает то, что способ оставил на диске (snmp.conf со строкой
// доступа); вызывать его при остановке обязательно.
func NewDialer(kind string, opts SNMPOptions) (dial Dialer, cleanup func(), err error) {
	switch kind {
	case "", WalkerSnmpwalk:
		// Программы нет — служба не стартует: иначе каждый проход честно
		// писал бы «устройство не ответило» про всю сеть.
		if _, err := exec.LookPath(SNMPProgram); err != nil {
			return nil, nil, fmt.Errorf("нет программы %s (net-snmp): %w", SNMPProgram, err)
		}
		dir, err := WriteSNMPConf(opts.Community)
		if err != nil {
			return nil, nil, err
		}
		return func(target string) (Walker, error) {
				return NewExecWalker(target, opts, dir)
			}, func() {
				_ = os.RemoveAll(dir)
			}, nil
	case WalkerGosnmp:
		if strings.TrimSpace(opts.Community) == "" {
			return nil, nil, fmt.Errorf("не задана строка доступа SNMP")
		}
		return func(target string) (Walker, error) {
			return NewLiveWalker(target, opts)
		}, func() {}, nil
	default:
		return nil, nil, fmt.Errorf("неизвестный способ опроса %q: ожидалось %s или %s",
			kind, WalkerSnmpwalk, WalkerGosnmp)
	}
}
