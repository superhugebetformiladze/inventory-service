// Package config читает настройки службы из окружения.
//
// Правило одно и оно жёсткое (инвариант I10): у секретов нет значений по
// умолчанию. Отсутствует переменная — служба не стартует и говорит, какой
// именно переменной не хватает. Умолчание для пароля однажды уезжает в
// продуктив и живёт там годами, пока его не найдут снаружи.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config — все настройки службы. Собирается один раз при старте и дальше
// только читается.
type Config struct {
	// Env — окружение: dev или prod. Влияет на подробность журнала.
	Env string

	// HTTPAddr — адрес, на котором служба слушает.
	HTTPAddr string

	// ShutdownTimeout — сколько ждать завершения текущих запросов при
	// остановке, прежде чем обрывать.
	ShutdownTimeout time.Duration

	// Postgres — параметры базы. Своей, не общей ни с кем (см. PLAN.md §1).
	PostgresHost string
	PostgresPort int
	PostgresUser string
	PostgresPass string
	PostgresDB   string

	// KafkaBrokers — адреса брокеров ленты через запятую.
	KafkaBrokers []string

	// LogLevel — debug, info, warn, error.
	LogLevel string

	// RelayInterval — как часто релей забирает накопившееся из outbox.
	// Прямо задаёт задержку доставки события потребителю.
	RelayInterval time.Duration

	// RelayBatchSize — сколько сообщений отправлять за раз. Отправка идёт
	// внутри транзакции базы, поэтому длинная пачка держит блокировки строк
	// дольше, чем нужно.
	RelayBatchSize int

	// SilenceThreshold — сколько машина может молчать, прежде чем это станет
	// событием. Заметно больше периода сбора: машина, выключенная на ночь,
	// не должна попадать в список каждое утро.
	SilenceThreshold time.Duration

	// SilenceInterval — как часто проверять, кто замолчал.
	SilenceInterval time.Duration

	// Сроки хранения. Ноль означает «как в программе» (см. retention).
	RetentionSnapshots time.Duration
	RetentionOutbox    time.Duration
	RetentionCommands  time.Duration
	RetentionInterval  time.Duration

	// EnrollBurst — сколько регистраций в минуту принимать с одного адреса.
	// Ноль означает «как в программе».
	EnrollBurst int

	// AdminToken — ключ к административным маршрутам: постановка заданий не
	// агентом, а другой системой.
	//
	// Необязателен, и это осознанно: пустое значение означает, что таких
	// маршрутов НЕТ вовсе. Открытый вход, через который можно поставить
	// задание любой машине парка, не должен получаться случайно — из-за
	// незаданной переменной или опечатки в имени.
	AdminToken string

	// Опрос сетевых устройств по SNMP.
	//
	// Список адресов пуст — опроса нет, и это обычное состояние: у службы
	// может не быть сетевого доступа к коммутаторам, и тогда снимки приходят
	// файлами (`agentctl netscan-import`). Задан список — обязательна строка
	// доступа (I10): опрос с пустой строкой молча отвечал бы «не отвечают все».
	NetscanTargets  []string
	SNMPCommunity   string
	NetscanInterval time.Duration
	NetscanTimeout  time.Duration
	// NetscanSilence — после скольких неудачных опросов подряд (по времени)
	// устройство считается молчащим; 0 — четыре периода опроса.
	NetscanSilence time.Duration
	SNMPTimeout    time.Duration
	SNMPRetries    int
	// NetscanWalker — чем опрашивать: snmpwalk (программа net-snmp, по
	// умолчанию) или gosnmp (встроенная библиотека).
	NetscanWalker string
}

// missing собирает имена незаданных обязательных переменных, чтобы сказать о
// всех сразу. Падать по одной за запуск — это пять перезапусков вместо одного.
type missing []string

func (m *missing) required(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		*m = append(*m, key)
	}
	return v
}

func optional(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Load читает окружение и проверяет его полноту.
func Load() (Config, error) {
	var m missing

	cfg := Config{
		Env:      optional("APP_ENV", "dev"),
		HTTPAddr: optional("HTTP_ADDR", ":8080"),
		LogLevel: optional("LOG_LEVEL", "info"),

		PostgresHost: m.required("POSTGRES_HOST"),
		PostgresUser: m.required("POSTGRES_USER"),
		PostgresPass: m.required("POSTGRES_PASSWORD"),
		PostgresDB:   m.required("POSTGRES_DB"),
	}

	port, err := strconv.Atoi(optional("POSTGRES_PORT", "5432"))
	if err != nil {
		return Config{}, fmt.Errorf("POSTGRES_PORT: ожидалось число, получено %q", os.Getenv("POSTGRES_PORT"))
	}
	cfg.PostgresPort = port

	brokers := m.required("KAFKA_BROKERS")
	for _, b := range strings.Split(brokers, ",") {
		if b = strings.TrimSpace(b); b != "" {
			cfg.KafkaBrokers = append(cfg.KafkaBrokers, b)
		}
	}

	timeout, err := time.ParseDuration(optional("SHUTDOWN_TIMEOUT", "15s"))
	if err != nil {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
	}
	cfg.ShutdownTimeout = timeout

	relayInterval, err := time.ParseDuration(optional("RELAY_INTERVAL", "1s"))
	if err != nil {
		return Config{}, fmt.Errorf("RELAY_INTERVAL: %w", err)
	}
	cfg.RelayInterval = relayInterval

	batch, err := strconv.Atoi(optional("RELAY_BATCH_SIZE", "100"))
	if err != nil {
		return Config{}, fmt.Errorf("RELAY_BATCH_SIZE: ожидалось число, получено %q", os.Getenv("RELAY_BATCH_SIZE"))
	}
	cfg.RelayBatchSize = batch

	silenceThreshold, err := time.ParseDuration(optional("SILENCE_THRESHOLD", "168h"))
	if err != nil {
		return Config{}, fmt.Errorf("SILENCE_THRESHOLD: %w", err)
	}
	cfg.SilenceThreshold = silenceThreshold

	silenceInterval, err := time.ParseDuration(optional("SILENCE_INTERVAL", "1h"))
	if err != nil {
		return Config{}, fmt.Errorf("SILENCE_INTERVAL: %w", err)
	}
	cfg.SilenceInterval = silenceInterval

	// Сроки хранения необязательны: без них действуют значения из
	// retention. Неверно записанный срок — ошибка, а не повод молча взять
	// умолчание: «RETENTION_SNAPSHOTS=30» без единицы означало бы 30
	// наносекунд.
	for _, поле := range []struct {
		имя  string
		цель *time.Duration
	}{
		{"RETENTION_SNAPSHOTS", &cfg.RetentionSnapshots},
		{"RETENTION_OUTBOX", &cfg.RetentionOutbox},
		{"RETENTION_COMMANDS", &cfg.RetentionCommands},
		{"RETENTION_INTERVAL", &cfg.RetentionInterval},
	} {
		значение := os.Getenv(поле.имя)
		if значение == "" {
			continue
		}
		d, err := time.ParseDuration(значение)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", поле.имя, err)
		}
		*поле.цель = d
	}

	if значение := os.Getenv("ENROLL_BURST"); значение != "" {
		n, err := strconv.Atoi(значение)
		if err != nil {
			return Config{}, fmt.Errorf("ENROLL_BURST: %w", err)
		}
		cfg.EnrollBurst = n
	}

	// Без значения по умолчанию и без попадания в список обязательных:
	// не задан — административных маршрутов нет.
	cfg.AdminToken = os.Getenv("ADMIN_TOKEN")

	for _, t := range strings.Split(os.Getenv("NETSCAN_TARGETS"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			cfg.NetscanTargets = append(cfg.NetscanTargets, t)
		}
	}
	// Список из файла — когда коммутаторов сотня: строка в .env на полторы
	// тысячи символов нечитаема и правится с ошибками. Файл указан, но не
	// читается — служба не стартует: молча опрашивать пустой список значило
	// бы неделю не замечать, что опрос стоит.
	if path := strings.TrimSpace(os.Getenv("NETSCAN_TARGETS_FILE")); path != "" {
		fromFile, err := readTargets(path)
		if err != nil {
			return Config{}, fmt.Errorf("NETSCAN_TARGETS_FILE: %w", err)
		}
		cfg.NetscanTargets = append(cfg.NetscanTargets, fromFile...)
	}
	if len(cfg.NetscanTargets) > 0 {
		cfg.SNMPCommunity = m.required("SNMP_COMMUNITY")
	} else {
		// Без списка в настройках строка доступа всё равно нужна: адреса
		// может прислать внешняя система (PUT /v1/admin/network/targets).
		// Задана — опрос включён, не задана — выключен.
		cfg.SNMPCommunity = os.Getenv("SNMP_COMMUNITY")
	}
	cfg.NetscanWalker = optional("NETSCAN_WALKER", "snmpwalk")
	if cfg.NetscanWalker != "snmpwalk" && cfg.NetscanWalker != "gosnmp" {
		return Config{}, fmt.Errorf("NETSCAN_WALKER: ожидалось snmpwalk или gosnmp, получено %q", cfg.NetscanWalker)
	}
	for _, поле := range []struct {
		имя       string
		умолчание string
		цель      *time.Duration
	}{
		{"NETSCAN_INTERVAL", "30m", &cfg.NetscanInterval},
		{"NETSCAN_TIMEOUT", "2m", &cfg.NetscanTimeout},
		{"SNMP_TIMEOUT", "5s", &cfg.SNMPTimeout},
		{"NETSCAN_SILENCE", "0s", &cfg.NetscanSilence},
	} {
		d, err := time.ParseDuration(optional(поле.имя, поле.умолчание))
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", поле.имя, err)
		}
		*поле.цель = d
	}
	retries, err := strconv.Atoi(optional("SNMP_RETRIES", "1"))
	if err != nil {
		return Config{}, fmt.Errorf("SNMP_RETRIES: ожидалось число, получено %q", os.Getenv("SNMP_RETRIES"))
	}
	cfg.SNMPRetries = retries

	if len(m) > 0 {
		return Config{}, fmt.Errorf("не заданы обязательные переменные окружения: %s", strings.Join(m, ", "))
	}

	return cfg, nil
}

// readTargets читает адреса по одному в строке; «#» — комментарий до конца строки.
func readTargets(path string) ([]string, error) {
	body, err := os.ReadFile(path) //nolint:gosec // путь задаёт администратор в .env
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if fields := strings.Fields(line); len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out, nil
}

// PostgresAddr — адрес базы для проверки готовности.
func (c Config) PostgresAddr() string {
	return fmt.Sprintf("%s:%d", c.PostgresHost, c.PostgresPort)
}
