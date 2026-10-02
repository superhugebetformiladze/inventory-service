package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Имена файлов в каталоге данных агента.
const (
	ConfigFile = "config.json"
	StateFile  = "state.json"
	LogFile    = "agent.log"
	SpoolDir   = "spool"
)

// DefaultDataDir — где агент держит своё.
//
// На Windows это ProgramData: каталог переживает смену пользователя и
// обновление системы, а права на него по умолчанию таковы, что читать может
// кто угодно, а писать — только администратор и система. Отметка агента и его
// ключи лежат именно там, и это единственная настоящая их защита: права
// самого файла на Windows условны.
func DefaultDataDir() string {
	if runtime.GOOS != "windows" {
		return "/var/lib/inventory-agent"
	}
	if dir := os.Getenv("ProgramData"); dir != "" {
		return filepath.Join(dir, "inventory-agent")
	}
	return `C:\ProgramData\inventory-agent`
}

// fileConfig — то, что лежит в config.json рядом с агентом.
type fileConfig struct {
	// Server — адрес службы. Единственная обязательная настройка.
	Server string `json:"server"`

	// IntervalSeconds — период сбора. Ноль означает «как в программе».
	IntervalSeconds int `json:"interval_seconds,omitempty"`

	// PollIntervalSeconds — как часто спрашивать про задания.
	//
	// Отсюда берётся время отклика кнопки «Обновить сейчас», и подходящее
	// значение зависит от парка: на сотне машин минута — это ничто, на
	// пяти тысячах — восемьдесят запросов в секунду впустую.
	PollIntervalSeconds int `json:"poll_interval_seconds,omitempty"`

	// EnrollmentToken — токен раскатки, положенный установщиком.
	//
	// Стирается сразу после удачной регистрации, и это не мелочь: токен
	// действует на все машины сразу, а установщик кладёт его в файл на
	// каждой из пятисот. Оставить его лежать — значит завести пятьсот копий
	// ключа от всего парка на машинах, до которых добирается любой, кто
	// добрался до одной.
	EnrollmentToken string `json:"enrollment_token,omitempty"`
}

// LoadConfig читает настройки агента.
//
// Отсутствие файла — не ошибка: адрес службы можно задать и флагом, а первый
// запуск установщика идёт именно так.
func LoadConfig(dataDir, version string) (Config, string, error) {
	cfg := Config{
		DataDir:      dataDir,
		Interval:     DefaultInterval,
		PollInterval: DefaultPollInterval,
		Tick:         DefaultTick,
		HTTPTimeout:  DefaultHTTPTimeout,
		Version:      version,
	}

	//nolint:gosec // каталог данных задаёт тот, кто ставит агента, — это и есть настройка
	b, err := os.ReadFile(filepath.Join(dataDir, ConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, "", nil
	}
	if err != nil {
		return cfg, "", fmt.Errorf("чтение настроек: %w", err)
	}

	var f fileConfig
	if err := json.Unmarshal(b, &f); err != nil {
		return cfg, "", fmt.Errorf("настройки не разбираются: %w", err)
	}

	cfg.Server = f.Server
	if f.IntervalSeconds > 0 {
		cfg.Interval = time.Duration(f.IntervalSeconds) * time.Second
	}
	if f.PollIntervalSeconds > 0 {
		cfg.PollInterval = time.Duration(f.PollIntervalSeconds) * time.Second
	}

	return cfg, f.EnrollmentToken, nil
}

// SaveConfig записывает настройки.
func SaveConfig(dataDir string, cfg Config, token string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("каталог данных: %w", err)
	}

	f := fileConfig{Server: cfg.Server, EnrollmentToken: token}
	if cfg.Interval > 0 && cfg.Interval != DefaultInterval {
		f.IntervalSeconds = int(cfg.Interval / time.Second)
	}
	if cfg.PollInterval > 0 && cfg.PollInterval != DefaultPollInterval {
		f.PollIntervalSeconds = int(cfg.PollInterval / time.Second)
	}

	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(dataDir, ConfigFile)
	temp := path + ".tmp"
	if err := os.WriteFile(temp, b, 0o600); err != nil {
		return fmt.Errorf("запись настроек: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// Paths — пути к файлам агента.
func (c Config) StatePath() string { return filepath.Join(c.DataDir, StateFile) }

// LogPath — путь к журналу.
func (c Config) LogPath() string { return filepath.Join(c.DataDir, LogFile) }

// SpoolPath — путь к очереди отчётов.
func (c Config) SpoolPath() string { return filepath.Join(c.DataDir, SpoolDir) }
