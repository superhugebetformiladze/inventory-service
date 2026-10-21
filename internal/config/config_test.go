package config

import (
	"os"
	"strings"
	"testing"
)

func setEnv(t *testing.T, pairs map[string]string) {
	t.Helper()
	for k, v := range pairs {
		t.Setenv(k, v)
	}
}

func полноеОкружение() map[string]string {
	return map[string]string{
		"POSTGRES_HOST":     "db",
		"POSTGRES_USER":     "inventory",
		"POSTGRES_PASSWORD": "секрет",
		"POSTGRES_DB":       "inventory",
		"KAFKA_BROKERS":     "redpanda:9092",
	}
}

func TestЗагрузкаПолногоОкружения(t *testing.T) {
	setEnv(t, полноеОкружение())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if cfg.PostgresAddr() != "db:5432" {
		t.Errorf("адрес базы: ожидался db:5432, получен %s", cfg.PostgresAddr())
	}
	if len(cfg.KafkaBrokers) != 1 || cfg.KafkaBrokers[0] != "redpanda:9092" {
		t.Errorf("брокеры разобраны неверно: %v", cfg.KafkaBrokers)
	}
}

func TestСекретБезЗначенияПоУмолчанию(t *testing.T) {
	// I10: отсутствует переменная — служба не стартует. Умолчание для пароля
	// однажды уезжает в продуктив и живёт там, пока его не найдут снаружи.
	env := полноеОкружение()
	delete(env, "POSTGRES_PASSWORD")
	setEnv(t, env)
	t.Setenv("POSTGRES_PASSWORD", "")

	_, err := Load()
	if err == nil {
		t.Fatal("ожидалась ошибка при отсутствии POSTGRES_PASSWORD")
	}
	if !strings.Contains(err.Error(), "POSTGRES_PASSWORD") {
		t.Errorf("ошибка должна называть переменную, получено: %v", err)
	}
}

func TestОшибкаНазываетВсеНедостающиеПеременныеСразу(t *testing.T) {
	// Падать по одной переменной за запуск — это пять перезапусков вместо
	// одного.
	setEnv(t, map[string]string{
		"POSTGRES_HOST":     "db",
		"POSTGRES_USER":     "",
		"POSTGRES_PASSWORD": "",
		"POSTGRES_DB":       "inventory",
		"KAFKA_BROKERS":     "redpanda:9092",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	for _, key := range []string{"POSTGRES_USER", "POSTGRES_PASSWORD"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("в ошибке нет %s: %v", key, err)
		}
	}
}

func TestНесколькоБрокеровЧерезЗапятую(t *testing.T) {
	env := полноеОкружение()
	env["KAFKA_BROKERS"] = "a:9092, b:9092 ,c:9092"
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if len(cfg.KafkaBrokers) != 3 {
		t.Fatalf("ожидалось 3 брокера, получено %d: %v", len(cfg.KafkaBrokers), cfg.KafkaBrokers)
	}
	if cfg.KafkaBrokers[1] != "b:9092" {
		t.Errorf("пробелы вокруг адреса не обрезаны: %q", cfg.KafkaBrokers[1])
	}
}

func TestОпросКоммутаторовТребуетСтрокуДоступа(t *testing.T) {
	// I10: список задан, строки доступа нет — служба не стартует. Опрос с
	// пустой строкой отвечал бы «не отвечают все» и выглядел бы как авария сети.
	setEnv(t, полноеОкружение())
	t.Setenv("NETSCAN_TARGETS", "192.168.8.10")
	t.Setenv("SNMP_COMMUNITY", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "SNMP_COMMUNITY") {
		t.Fatalf("ожидалась ошибка про SNMP_COMMUNITY, получено %v", err)
	}
}

func TestСписокКоммутаторовИзФайла(t *testing.T) {
	path := t.TempDir() + "/switches.txt"
	body := "# ядро первым\n192.168.8.10   # DGS-3130\n\n192.168.4.172\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	setEnv(t, полноеОкружение())
	t.Setenv("NETSCAN_TARGETS", "10.0.0.1")
	t.Setenv("NETSCAN_TARGETS_FILE", path)
	t.Setenv("SNMP_COMMUNITY", "секрет")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.1", "192.168.8.10", "192.168.4.172"}
	if strings.Join(cfg.NetscanTargets, ",") != strings.Join(want, ",") {
		t.Errorf("адреса: %v, ожидались %v", cfg.NetscanTargets, want)
	}
}

func TestНечитаемыйФайлКоммутаторов(t *testing.T) {
	setEnv(t, полноеОкружение())
	t.Setenv("NETSCAN_TARGETS_FILE", "/нет/такого/файла")

	if _, err := Load(); err == nil {
		t.Fatal("указанный, но нечитаемый файл должен останавливать старт")
	}
}
