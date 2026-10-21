package netscan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/broton07/inventory_service/internal/storage"
)

// подставнаяПрограмма — вместо snmpbulkwalk: записывает, с чем её вызвали,
// и печатает заданный вывод. Настоящая программа требует настоящего
// коммутатора; здесь проверяется то, что делает служба, — аргументы,
// окружение, разбор вывода и ошибок.
func подставнаяПрограмма(t *testing.T, stdout, stderr string, code int) (program, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "вызов")
	out := filepath.Join(dir, "out")
	if err := os.WriteFile(out, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"echo \"ARGS: $*\" >> " + log + "\n" +
		"echo \"CONF: $SNMPCONFPATH\" >> " + log + "\n" +
		"cat " + out + "\n" +
		"printf '%s' '" + stderr + "' >&2\n" +
		"exit " + string(rune('0'+code)) + "\n"
	program = filepath.Join(dir, "snmpbulkwalk")
	if err := os.WriteFile(program, []byte(script), 0o700); err != nil { //nolint:gosec // исполняемый файл теста
		t.Fatal(err)
	}
	return program, log
}

func execWalker(t *testing.T, program string) *ExecWalker {
	t.Helper()
	dir, err := WriteSNMPConf("секрет\r")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	w, err := NewExecWalker("192.168.4.172", SNMPOptions{Timeout: 3 * time.Second, Retries: 1}, dir)
	if err != nil {
		t.Fatal(err)
	}
	w.program = program
	return w
}

func TestSnmpwalkДаётТотЖеСнимок(t *testing.T) {
	walk, err := os.ReadFile("testdata/192.168.4.172.walk")
	if err != nil {
		t.Fatal(err)
	}
	program, log := подставнаяПрограмма(t, string(walk), "", 0)

	dev, err := Collect(context.Background(), execWalker(t, program), "192.168.4.172", момент)
	if err != nil {
		t.Fatal(err)
	}
	want := собрать(t, "192.168.4.172.walk", "192.168.4.172")
	if dev.DeviceID != want.DeviceID || len(dev.Ports) != len(want.Ports) {
		t.Errorf("снимок через программу: %s, портов %d; из файла: %s, портов %d",
			dev.DeviceID, len(dev.Ports), want.DeviceID, len(want.Ports))
	}
	if a, b := порт(t, dev, 1).MACs, порт(t, want, 1).MACs; strings.Join(a, ",") != strings.Join(b, ",") {
		t.Errorf("MAC на порту 1: %v, ожидалось %v", a, b)
	}

	called, err := os.ReadFile(log) //nolint:gosec // файл теста
	if err != nil {
		t.Fatal(err)
	}
	text := string(called)
	for _, need := range []string{"-v2c", "-On", "-Ox", "-m  ", "-t 3", "-r 1", "-Cr25", "udp:192.168.4.172:161"} {
		if !strings.Contains(text, need) {
			t.Errorf("в вызове нет %q:\n%s", need, text)
		}
	}
	// Строка доступа — не в аргументах: их видно всем через ps.
	if strings.Contains(text, "секрет") {
		t.Errorf("строка доступа попала в аргументы:\n%s", text)
	}
	if !strings.Contains(text, "CONF: "+os.TempDir()) && !strings.Contains(text, "CONF: /") {
		t.Errorf("программе не передан каталог настроек:\n%s", text)
	}
}

func TestSnmpConfТолькоВладельцу(t *testing.T) {
	dir, err := WriteSNMPConf(" секрет\r\n")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	body, err := os.ReadFile(filepath.Join(dir, "snmp.conf")) //nolint:gosec // файл теста
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "defVersion 2c\ndefCommunity секрет\n" {
		t.Errorf("snmp.conf: %q", body)
	}
	info, err := os.Stat(filepath.Join(dir, "snmp.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права snmp.conf: %v", info.Mode().Perm())
	}
	if _, err := WriteSNMPConf("a\nb"); err == nil {
		t.Error("перевод строки внутри строки доступа дописал бы в snmp.conf свою директиву")
	}
}

func TestSnmpwalkНеОтветилИОбъяснилПочему(t *testing.T) {
	program, _ := подставнаяПрограмма(t, "", "Timeout: No Response from udp:192.168.4.172:161\n", 1)

	_, err := Collect(context.Background(), execWalker(t, program), "192.168.4.172", момент)
	if err == nil || !strings.Contains(err.Error(), "Timeout: No Response") {
		t.Errorf("ожидалась ошибка со словами программы, получено %v", err)
	}
}

func TestНеизвестныйСпособОпроса(t *testing.T) {
	if _, _, err := NewDialer("telnet", SNMPOptions{Community: "x"}); err == nil {
		t.Error("ожидалась ошибка")
	}
	if _, _, err := NewDialer(WalkerGosnmp, SNMPOptions{}); err == nil {
		t.Error("без строки доступа опрос не готовится")
	}
}

// ------------------------------------------------------------------
// Список опроса из базы
// ------------------------------------------------------------------

type списокОпроса struct {
	адреса  []string
	попытки map[string]string // адрес → ошибка или "ok:<device_id>"
	сломан  bool
}

func (l *списокОпроса) ListNetworkTargets(context.Context) ([]storage.NetworkTargetRow, error) {
	if l.сломан {
		return nil, errors.New("база недоступна")
	}
	out := make([]storage.NetworkTargetRow, 0, len(l.адреса))
	for _, ip := range l.адреса {
		out = append(out, storage.NetworkTargetRow{IP: ip})
	}
	return out, nil
}

func (l *списокОпроса) RecordNetworkAttempt(_ context.Context, ip, deviceID, errText string, _ time.Time) error {
	if l.попытки == nil {
		l.попытки = map[string]string{}
	}
	if errText != "" {
		l.попытки[ip] = errText
	} else {
		l.попытки[ip] = "ok:" + deviceID
	}
	return nil
}

func TestПроходБерётАдресаИзБазыИПишетИтог(t *testing.T) {
	store := &памятьУстройств{}
	list := &списокОпроса{адреса: []string{"192.168.4.172", "192.168.4.250"}}
	var опрошены []string

	runner := NewRunner(NewService(store, func() time.Time { return момент }),
		func(target string) (Walker, error) {
			опрошены = append(опрошены, target)
			if target == "192.168.4.172" {
				return обход(t, "192.168.4.172.walk"), nil
			}
			return &FileWalker{}, nil // молчит
		},
		RunnerOptions{
			// Из настроек и из базы один и тот же — опрашивается один раз.
			Targets: []string{"192.168.4.172"},
			List:    list,
			Now:     func() time.Time { return момент },
		})

	stats := runner.Tick(context.Background())
	if stats.Polled != 1 || stats.Failed != 1 {
		t.Errorf("итог прохода: %+v", stats)
	}
	if strings.Join(опрошены, ",") != "192.168.4.172,192.168.4.250" {
		t.Errorf("опрошены: %v", опрошены)
	}
	if got := list.попытки["192.168.4.172"]; got != "ok:88:76:b9:c8:89:80" {
		t.Errorf("итог ответившего: %q", got)
	}
	if got := list.попытки["192.168.4.250"]; got == "" || strings.HasPrefix(got, "ok:") {
		t.Errorf("итог молчащего: %q", got)
	}

	// Новый список — со следующего прохода, без перезапуска.
	list.адреса = nil
	опрошены = nil
	runner.Tick(context.Background())
	if strings.Join(опрошены, ",") != "192.168.4.172" {
		t.Errorf("после замены списка опрошены: %v", опрошены)
	}

	// База упала — опрашивается хотя бы то, что в настройках.
	list.сломан = true
	опрошены = nil
	runner.Tick(context.Background())
	if strings.Join(опрошены, ",") != "192.168.4.172" {
		t.Errorf("без базы опрошены: %v", опрошены)
	}
}

func TestВнеочередноеНажатиеНеКопится(t *testing.T) {
	runner := NewRunner(NewService(&памятьУстройств{}, nil),
		func(string) (Walker, error) { return &FileWalker{}, nil }, RunnerOptions{})
	if !runner.Kick() {
		t.Error("первая просьба встаёт в очередь")
	}
	if runner.Kick() {
		t.Error("вторая, пока первая ждёт, — нет")
	}
}
