// Package agent — программа, которая живёт на рабочей станции.
//
// Всё, что здесь есть, устроено вокруг одного: машина может быть выключена,
// без сети, в командировке или за NAT, и служба к ней не стучится никогда.
// Агент сам решает, когда собрать сведения, сам решает, что делать, если
// служба недоступна, и сам приходит за заданиями.
package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/pkg/contract"
)

// State — то, что агент обязан помнить между запусками.
//
// Отметка здесь — самое ценное. Она первое звено цепочки опознания: пока она
// цела, переименование машины, замена диска и переустановка системы не
// заводят в учёте вторую карточку. Потерять её файл — значит превратить
// машину в новую.
type State struct {
	// AgentID — отметка агента, придуманная при первом запуске.
	AgentID string `json:"agent_id"`

	// AgentKey — персональный ключ машины, полученный в обмен на токен
	// раскатки. Пуст, пока регистрация не прошла.
	AgentKey string `json:"agent_key"`

	// PrivateKeySeed — зерно закрытого ключа, base64. Ed25519 восстанавливает
	// ключ из 32 байт, и хранить все 64 незачем.
	PrivateKeySeed string `json:"private_key_seed"`

	// IntervalSeconds — период сбора, если его меняли заданием. Ноль
	// означает «как в настройках».
	IntervalSeconds int `json:"interval_seconds,omitempty"`

	EnrolledAt time.Time `json:"enrolled_at,omitempty"`
}

// Enrolled — прошла ли регистрация.
func (s State) Enrolled() bool { return s.AgentKey != "" }

// PrivateKey восстанавливает закрытый ключ из зерна.
func (s State) PrivateKey() (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(s.PrivateKeySeed)
	if err != nil {
		return nil, fmt.Errorf("зерно закрытого ключа не разбирается: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("зерно закрытого ключа длиной %d байт вместо %d",
			len(seed), ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// PublicKey — открытый ключ машины.
func (s State) PublicKey() (ed25519.PublicKey, error) {
	priv, err := s.PrivateKey()
	if err != nil {
		return nil, err
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("закрытый ключ не даёт открытого")
	}
	return pub, nil
}

// Interval — период сбора с учётом того, что могли поменять заданием.
func (s State) Interval(fallback time.Duration) time.Duration {
	if s.IntervalSeconds <= 0 {
		return fallback
	}
	d := time.Duration(s.IntervalSeconds) * time.Second
	// Пределы проверяются ещё раз здесь, а не только при получении задания:
	// файл состояния может быть поправлен руками, и период в одну секунду
	// превратил бы машину в источник постоянной нагрузки.
	if d < contract.MinCollectInterval || d > contract.MaxCollectInterval {
		return fallback
	}
	return d
}

// NewState заводит состояние при первом запуске.
func NewState() (State, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return State{}, fmt.Errorf("генерация ключа машины: %w", err)
	}

	// Отметка случайна и никак не связана ни с именем машины, ни с её
	// оборудованием. Связать её было бы удобно, но тогда переименование или
	// замена платы меняли бы отметку — то есть ровно то, ради чего она и
	// заведена, перестало бы работать.
	return State{
		AgentID:        uuid.NewString(),
		PrivateKeySeed: base64.StdEncoding.EncodeToString(priv.Seed()),
	}, nil
}

// LoadState читает состояние с диска.
//
// Отсутствие файла — не ошибка: так выглядит первый запуск.
func LoadState(path string) (State, bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // путь состояния задаётся настройкой каталога данных
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("чтение состояния: %w", err)
	}

	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		// Испорченный файл НЕ заменяется новым состоянием молча: новое
		// состояние — это новая отметка, то есть вторая карточка той же
		// машины в учёте. Пусть лучше агент откажется работать и это увидит
		// человек.
		return State{}, false, fmt.Errorf("состояние испорчено (%s): %w", path, err)
	}
	if s.AgentID == "" {
		return State{}, false, fmt.Errorf("в состоянии (%s) нет отметки агента", path)
	}

	return s, true, nil
}

// SaveState записывает состояние.
//
// Через временный файл и переименование: запись поверх может оборваться на
// половине — выключили питание, — и агент останется без отметки. Переименование
// внутри одного каталога либо происходит целиком, либо не происходит вовсе.
func SaveState(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("каталог состояния: %w", err)
	}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	temp := path + ".tmp"
	// 0600: в файле лежит закрытый ключ машины и её ключ доступа. На Windows
	// права файла условны, и настоящая защита — права каталога ProgramData,
	// куда посторонним записи нет.
	if err := os.WriteFile(temp, b, 0o600); err != nil {
		return fmt.Errorf("запись состояния: %w", err)
	}

	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("замена состояния: %w", err)
	}
	return nil
}
