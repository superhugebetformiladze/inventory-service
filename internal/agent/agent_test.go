package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/broton07/inventory_service/pkg/contract"
)

var сейчас = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// служба изображает сервер: принимает отчёты, выдаёт задания и умеет
// «падать», чтобы можно было проверить поведение без сети.
type служба struct {
	mu sync.Mutex

	принято  []contract.Snapshot
	задания  []contract.Command
	закрыты  map[string]contract.CommandResult
	отвечать int // код ответа на отчёт; 0 — принимать

	server *httptest.Server
}

func новаяСлужба(t *testing.T) *служба {
	t.Helper()

	s := &служба{закрыты: map[string]contract.CommandResult{}}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/reports", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		if s.отвечать != 0 {
			w.WriteHeader(s.отвечать)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "нет"})
			return
		}

		var snap contract.Snapshot
		if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.принято = append(s.принято, snap)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	})

	mux.HandleFunc("GET /v1/commands", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		list := s.задания
		s.задания = nil // задание выдаётся один раз, как у настоящей службы
		if list == nil {
			list = []contract.Command{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"commands": list})
	})

	mux.HandleFunc("POST /v1/commands/{id}/result", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		var result contract.CommandResult
		_ = json.NewDecoder(r.Body).Decode(&result)
		s.закрыты[r.PathValue("id")] = result
		w.WriteHeader(http.StatusNoContent)
	})

	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func (s *служба) отвечатьКодом(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.отвечать = code
}

func (s *служба) упасть() { s.server.Close() }

func (s *служба) сколькоПринято() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.принято)
}

func (s *служба) поставитьЗадание(cmd contract.Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.задания = append(s.задания, cmd)
}

func (s *служба) результат(id string) (contract.CommandResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.закрыты[id]
	return r, ok
}

// снимок — минимальный годный снимок машины.
func снимок(момент time.Time) contract.Snapshot {
	return contract.Snapshot{
		SchemaVersion: contract.SchemaVersion,
		AgentVersion:  "тест",
		CollectedAt:   момент,
		Origin:        contract.OriginNetwork,
		Machine:       contract.Machine{Hostname: "PC-TEST", Serial: "SN-000123"},
	}
}

// стенд — агент со своим каталогом и подменённым сбором.
type стенд struct {
	agent  *Agent
	spool  *Spool
	dir    string
	момент time.Time
}

func новыйСтенд(t *testing.T, s *служба) *стенд {
	t.Helper()

	dir := t.TempDir()
	state, err := NewState()
	if err != nil {
		t.Fatalf("состояние: %v", err)
	}
	state.AgentKey = "inv_" + "тестовыйключдлиннеешестнадцати"

	statePath := filepath.Join(dir, "state.json")
	if err := SaveState(statePath, state); err != nil {
		t.Fatalf("сохранение состояния: %v", err)
	}

	ст := &стенд{
		spool:  &Spool{Dir: filepath.Join(dir, "spool"), Max: 5},
		dir:    dir,
		момент: сейчас,
	}

	адрес := ""
	if s != nil {
		адрес = s.server.URL
	}

	ст.agent = New(Config{
		Server:      адрес,
		Version:     "тест",
		HTTPTimeout: 2 * time.Second,
	}, state, statePath, ст.spool,
		func(_ context.Context, mark, version string, origin contract.Origin) contract.Snapshot {
			snap := снимок(ст.момент)
			snap.AgentID = mark
			snap.AgentVersion = version
			snap.Origin = origin
			return snap
		},
		slog.New(slog.DiscardHandler))

	ст.agent.now = func() time.Time { return ст.момент }
	return ст
}

func TestОтчётУходитКогдаСлужбаДоступна(t *testing.T) {
	s := новаяСлужба(t)
	ст := новыйСтенд(t, s)

	ст.agent.CollectAndSend(t.Context(), ст.момент)

	if got := s.сколькоПринято(); got != 1 {
		t.Fatalf("служба получила %d отчётов, ожидался 1", got)
	}
	if ст.spool.Len() != 0 {
		t.Errorf("отчёт остался в очереди, хотя доставлен")
	}
}

func TestБезСетиОтчётОткладываетсяИДосылается(t *testing.T) {
	// Ради этого агента и заводили: без буфера отчёт машины, у которой в
	// момент сбора не было сети, просто пропадал бы.
	s := новаяСлужба(t)
	ст := новыйСтенд(t, s)

	s.упасть()
	ст.agent.CollectAndSend(t.Context(), ст.момент)

	if ст.spool.Len() != 1 {
		t.Fatalf("отчёт не отложен: в очереди %d", ст.spool.Len())
	}
	if ст.agent.nextRetry.IsZero() {
		t.Error("повтор не назначен")
	}

	// Сеть вернулась: поднимаем службу на том же адресе.
	вторая := новаяСлужба(t)
	ст.agent.client.BaseURL = вторая.server.URL

	ст.момент = ст.момент.Add(time.Hour)
	ст.agent.drain(t.Context(), ст.момент)

	if got := вторая.сколькоПринято(); got != 1 {
		t.Fatalf("отложенный отчёт не доставлен: принято %d", got)
	}
	if ст.spool.Len() != 0 {
		t.Errorf("доставленный отчёт остался в очереди")
	}
	if !ст.agent.nextRetry.IsZero() || ст.agent.failures != 0 {
		t.Error("после удачной досылки счётчик неудач не сброшен")
	}
}

func TestОтвергнутыйОтчётНеКопится(t *testing.T) {
	// Отчёт, признанный негодным, при повторе будет отвергнут снова. Агент,
	// который этого не понимает, забивает им очередь навсегда и перестаёт
	// отправлять всё остальное.
	s := новаяСлужба(t)
	ст := новыйСтенд(t, s)

	s.отвечатьКодом(http.StatusBadRequest)
	ст.agent.CollectAndSend(t.Context(), ст.момент)

	if ст.spool.Len() != 0 {
		t.Fatalf("отвергнутый отчёт отложен: в очереди %d", ст.spool.Len())
	}
}

func TestОтозванныйКлючНеПревращаетсяВОчередь(t *testing.T) {
	// Ключ отозвали. Копить отчёты бессмысленно: их не примут и завтра.
	s := новаяСлужба(t)
	ст := новыйСтенд(t, s)

	s.отвечатьКодом(http.StatusUnauthorized)
	ст.agent.CollectAndSend(t.Context(), ст.момент)

	if ст.spool.Len() != 0 {
		t.Fatalf("отчёт отложен, хотя ключ не признан: в очереди %d", ст.spool.Len())
	}
}

func TestЗадержкаПовтораРастётИОграничена(t *testing.T) {
	// Служба, лежащая под нагрузкой, не должна получать от пятисот агентов
	// по попытке в минуту: это добивает её ровно тогда, когда она пытается
	// подняться.
	ст := новыйСтенд(t, nil)

	var прошлая time.Duration
	for попытка := 1; попытка <= 12; попытка++ {
		ст.agent.failure(ст.момент)
		задержка := ст.agent.nextRetry.Sub(ст.момент)

		if задержка < minBackoff/2 {
			t.Fatalf("попытка %d: задержка %s меньше разумного", попытка, задержка)
		}
		if задержка > maxBackoff+maxBackoff/10 {
			t.Fatalf("попытка %d: задержка %s больше предела", попытка, задержка)
		}
		if попытка > 1 && попытка < 6 && задержка <= прошлая {
			t.Errorf("попытка %d: задержка %s не выросла (была %s)", попытка, задержка, прошлая)
		}
		прошлая = задержка
	}
}

func TestОчередьНеРастётБезПредела(t *testing.T) {
	// Машина, простоявшая полгода без сети, иначе завалила бы службу
	// сотнями отчётов разом.
	ст := новыйСтенд(t, nil)

	for i := range 12 {
		snap := снимок(ст.момент.Add(time.Duration(i) * time.Hour))
		if err := ст.spool.Add(snap); err != nil {
			t.Fatalf("запись в очередь: %v", err)
		}
	}

	if got := ст.spool.Len(); got != 5 {
		t.Fatalf("в очереди %d отчётов, ожидалось 5", got)
	}

	// Выброшены должны быть САМЫЕ СТАРЫЕ: свежие сведения нужнее прошлых.
	files, _ := ст.spool.Files()
	первый, err := ст.spool.Read(files[0])
	if err != nil {
		t.Fatalf("чтение очереди: %v", err)
	}
	if !первый.CollectedAt.Equal(ст.момент.Add(7 * time.Hour)) {
		t.Errorf("в очереди остался отчёт от %s, ожидался от %s",
			первый.CollectedAt, ст.момент.Add(7*time.Hour))
	}
}

func TestУстаревшиеОтчётыВыбрасываются(t *testing.T) {
	// Служба их всё равно не примет, а агент считал бы отказ неполадкой
	// связи и повторял вечно.
	ст := новыйСтенд(t, nil)

	_ = ст.spool.Add(снимок(ст.момент.Add(-MaxSpoolAge - 24*time.Hour)))
	_ = ст.spool.Add(снимок(ст.момент))

	dropped, err := ст.spool.DropOlderThan(ст.момент, MaxSpoolAge)
	if err != nil {
		t.Fatalf("чистка очереди: %v", err)
	}
	if dropped != 1 || ст.spool.Len() != 1 {
		t.Errorf("выброшено %d, в очереди осталось %d", dropped, ст.spool.Len())
	}
}

func TestЗаданиеСобратьСейчасВыполняется(t *testing.T) {
	s := новаяСлужба(t)
	ст := новыйСтенд(t, s)

	s.поставитьЗадание(contract.Command{
		SchemaVersion: contract.SchemaVersion,
		ID:            "задание-1",
		Kind:          contract.CommandCollectNow,
		CreatedAt:     ст.момент,
	})

	ст.agent.poll(t.Context())

	if got := s.сколькоПринято(); got != 1 {
		t.Fatalf("после задания принято %d отчётов, ожидался 1", got)
	}

	result, ok := s.результат("задание-1")
	if !ok {
		t.Fatal("агент не сообщил о выполнении задания")
	}
	if !result.OK {
		t.Errorf("задание объявлено невыполненным: %s", result.Error)
	}
}

func TestЗаданиеМеняетПериодИЭтоПереживаетПерезапуск(t *testing.T) {
	// Без записи на диск после первой же перезагрузки машина вернулась бы к
	// прежнему периоду, а человек считал бы настройку применённой.
	s := новаяСлужба(t)
	ст := новыйСтенд(t, s)

	const шестьЧасов = 6 * 60 * 60

	s.поставитьЗадание(contract.Command{
		SchemaVersion: contract.SchemaVersion,
		ID:            "задание-2",
		Kind:          contract.CommandSetInterval,
		Params:        map[string]any{"interval_seconds": шестьЧасов},
		CreatedAt:     ст.момент,
	})

	ст.agent.poll(t.Context())

	if got := ст.agent.interval(); got != 6*time.Hour {
		t.Errorf("период в памяти: %s, ожидалось 6h", got)
	}

	// То, что увидит агент после перезапуска.
	снова, есть, err := LoadState(ст.agent.statePath)
	if err != nil || !есть {
		t.Fatalf("состояние не читается: %v", err)
	}
	if got := снова.Interval(DefaultInterval); got != 6*time.Hour {
		t.Errorf("период после перезапуска: %s, ожидалось 6h", got)
	}
}

func TestАгентОтказываетсяОтНеизвестногоЗадания(t *testing.T) {
	// Инвариант I6. Проверка стоит на СТОРОНЕ АГЕНТА тем же кодом контракта,
	// которым проверяет служба: агент не должен доверять тому, что служба
	// прислала правильное, иначе взломанная служба становится пультом
	// управления всем парком.
	s := новаяСлужба(t)
	ст := новыйСтенд(t, s)

	s.поставитьЗадание(contract.Command{
		SchemaVersion: contract.SchemaVersion,
		ID:            "злое",
		Kind:          contract.CommandKind("run_shell"),
		Params:        map[string]any{"cmd": "format c:"},
		CreatedAt:     ст.момент,
	})

	ст.agent.poll(t.Context())

	if _, закрыто := s.результат("злое"); закрыто {
		t.Error("агент взялся выполнять задание неизвестного вида")
	}
	if got := s.сколькоПринято(); got != 0 {
		t.Errorf("неизвестное задание что-то запустило: принято %d отчётов", got)
	}
}

func TestНакопленноеУходитПередСвежим(t *testing.T) {
	// Служба сравнивает снимки по времени сбора и отбрасывает пришедшие
	// позже, но снятые раньше. Отправить свежий вперёд накопленного — значит
	// потерять всю историю простоя.
	s := новаяСлужба(t)
	ст := новыйСтенд(t, s)

	_ = ст.spool.Add(снимок(ст.момент.Add(-2 * time.Hour)))
	_ = ст.spool.Add(снимок(ст.момент.Add(-1 * time.Hour)))

	ст.agent.CollectAndSend(t.Context(), ст.момент)

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.принято) != 3 {
		t.Fatalf("принято %d отчётов, ожидалось 3", len(s.принято))
	}
	for i := 1; i < len(s.принято); i++ {
		if s.принято[i].CollectedAt.Before(s.принято[i-1].CollectedAt) {
			t.Errorf("отчёты пришли не по порядку: %s после %s",
				s.принято[i].CollectedAt, s.принято[i-1].CollectedAt)
		}
	}
}

func TestРасписаниеРазбросаноПоМашинам(t *testing.T) {
	// Пятьсот агентов, запущенных групповой политикой в одну минуту, иначе
	// ходили бы в службу строем: раз в четыре часа все разом.
	ст := новыйСтенд(t, nil)

	видели := map[time.Duration]bool{}
	for range 50 {
		видели[ст.agent.jitter(time.Hour)] = true
	}
	if len(видели) < 10 {
		t.Errorf("разброс почти не работает: %d различных значений из 50", len(видели))
	}

	for d := range видели {
		if d < 54*time.Minute || d > 66*time.Minute {
			t.Errorf("разброс вышел за десятую часть: %s", d)
		}
	}
}
