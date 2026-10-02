package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

func TestПервыйЗапускЗаводитОтметкуИКлюч(t *testing.T) {
	s, err := NewState()
	if err != nil {
		t.Fatalf("новое состояние: %v", err)
	}

	if s.AgentID == "" {
		t.Error("отметка агента не заведена")
	}
	if s.Enrolled() {
		t.Error("машина считается зарегистрированной до регистрации")
	}

	priv, err := s.PrivateKey()
	if err != nil {
		t.Fatalf("закрытый ключ: %v", err)
	}
	pub, err := s.PublicKey()
	if err != nil {
		t.Fatalf("открытый ключ: %v", err)
	}

	// Пара должна быть настоящей: на ней держится подпись автономных отчётов.
	snap := contract.Snapshot{
		SchemaVersion: contract.SchemaVersion,
		AgentID:       s.AgentID,
		CollectedAt:   time.Now().UTC(),
		Origin:        contract.OriginOffline,
		Machine:       contract.Machine{Hostname: "PC"},
	}
	signed, err := contract.SignSnapshot(snap, priv)
	if err != nil {
		t.Fatalf("подпись: %v", err)
	}
	if _, err := signed.Verify(pub); err != nil {
		t.Errorf("подпись не проверяется своим же ключом: %v", err)
	}
}

func TestСостояниеПереживаетЗаписьИЧтение(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	было, _ := NewState()
	было.AgentKey = "inv_ключ"
	было.IntervalSeconds = 3600

	if err := SaveState(path, было); err != nil {
		t.Fatalf("запись: %v", err)
	}

	стало, есть, err := LoadState(path)
	if err != nil || !есть {
		t.Fatalf("чтение: %v", err)
	}
	if стало.AgentID != было.AgentID || стало.AgentKey != было.AgentKey {
		t.Error("состояние прочиталось не тем, чем записалось")
	}
	if стало.PrivateKeySeed != было.PrivateKeySeed {
		t.Error("закрытый ключ не сохранился")
	}
}

func TestПервогоЗапускаОтсутствиеФайлаНеОшибка(t *testing.T) {
	_, есть, err := LoadState(filepath.Join(t.TempDir(), "нет.json"))
	if err != nil {
		t.Fatalf("отсутствие файла принято за ошибку: %v", err)
	}
	if есть {
		t.Error("несуществующее состояние объявлено найденным")
	}
}

func TestИспорченноеСостояниеНеПодменяетсяМолча(t *testing.T) {
	// Новое состояние — это новая отметка, то есть вторая карточка той же
	// машины в учёте. Пусть лучше агент откажется работать и это увидит
	// человек.
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{это не json"), 0o600); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	if _, _, err := LoadState(path); err == nil {
		t.Fatal("испорченное состояние принято без возражений")
	}
}

func TestПериодСбораИзФайлаПроверяетсяНаПределы(t *testing.T) {
	// Файл состояния можно поправить руками, и период в одну секунду
	// превратил бы машину в источник постоянной нагрузки.
	случаи := map[string]struct {
		секунды  int
		ожидание time.Duration
	}{
		"не задан":      {0, DefaultInterval},
		"слишком часто": {1, DefaultInterval},
		"слишком редко": {60 * 60 * 24 * 30, DefaultInterval},
		"разумный":      {6 * 60 * 60, 6 * time.Hour},
		"отрицательный": {-1, DefaultInterval},
	}

	for имя, с := range случаи {
		s := State{IntervalSeconds: с.секунды}
		if got := s.Interval(DefaultInterval); got != с.ожидание {
			t.Errorf("%s: получено %s, ожидалось %s", имя, got, с.ожидание)
		}
	}
}
