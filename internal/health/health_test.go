package health

import (
	"context"
	"testing"
	"time"

	"github.com/broton07/inventory_service/internal/storage"
)

type источник struct {
	stats storage.Stats
}

func (и *источник) Snapshot(_ context.Context) (storage.Stats, error) {
	return и.stats, nil
}

var сейчас = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func TestЗдороваяСлужбаНеСообщаетНиОЧём(t *testing.T) {
	// Список неполадок должен быть пуст, а не содержать «всё в порядке»:
	// экран, на котором всегда что-то написано, перестают читать.
	src := &источник{stats: storage.Stats{
		Hosts:        500,
		HostsSilent:  12,
		OldestReport: 3 * time.Minute,
		LastReport:   30 * time.Second,
	}}

	r, err := Check(t.Context(), src, сейчас)
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if len(r.Problems) != 0 {
		t.Errorf("здоровая служба сообщила о %d неполадках: %+v", len(r.Problems), r.Problems)
	}
}

func TestОтставшаяЛентаЭтоСамоеВажное(t *testing.T) {
	src := &источник{stats: storage.Stats{
		Hosts:         10,
		OutboxPending: 40,
		OutboxLag:     20 * time.Minute,
	}}

	r, _ := Check(t.Context(), src, сейчас)

	if len(r.Problems) != 1 {
		t.Fatalf("неполадок %d, ожидалась одна: %+v", len(r.Problems), r.Problems)
	}
	if r.Problems[0].Level != LevelCritical {
		t.Errorf("уровень %q, ожидался %q", r.Problems[0].Level, LevelCritical)
	}
}

func TestПустойПаркНеСчитаетсяНеполадкой(t *testing.T) {
	// Службу только что подняли: машин нет, отчётов нет. Сообщать тут не о
	// чем, а деление на ноль в доле молчащих — тем более.
	src := &источник{stats: storage.Stats{}}

	r, err := Check(t.Context(), src, сейчас)
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if len(r.Problems) != 0 {
		t.Errorf("пустая служба сообщила о неполадках: %+v", r.Problems)
	}
}

func TestМолчаниеСчитаетсяВДоляхАНеВШтуках(t *testing.T) {
	// Пять молчащих из пятисот — это отпуска и ремонт; пять из десяти —
	// сломанная раскатка. Порог в штуках не различил бы эти два случая.
	большой := &источник{stats: storage.Stats{Hosts: 500, HostsSilent: 5}}
	if r, _ := Check(t.Context(), большой, сейчас); len(r.Problems) != 0 {
		t.Errorf("пять молчащих из пятисот — не неполадка: %+v", r.Problems)
	}

	маленький := &источник{stats: storage.Stats{Hosts: 10, HostsSilent: 5}}
	r, _ := Check(t.Context(), маленький, сейчас)
	if len(r.Problems) != 1 || r.Problems[0].Code != "парк-замолчал" {
		t.Errorf("пять молчащих из десяти — неполадка, получено: %+v", r.Problems)
	}
}

func TestУКаждойНеполадкиЕстьЧтоДелать(t *testing.T) {
	// Тревога без ответа на вопрос «и что теперь» через месяц перестаёт
	// читаться вместе с полезными.
	src := &источник{stats: storage.Stats{
		Hosts:        10,
		HostsSilent:  9,
		Unresolved:   80,
		OutboxLag:    time.Hour,
		OldestReport: 4 * time.Hour,
		LastReport:   4 * time.Hour,
	}}

	r, _ := Check(t.Context(), src, сейчас)
	if len(r.Problems) != 4 {
		t.Fatalf("неполадок %d, ожидались все четыре: %+v", len(r.Problems), r.Problems)
	}
	for _, p := range r.Problems {
		if p.Summary == "" || p.Advice == "" {
			t.Errorf("неполадка %q без объяснения: %+v", p.Code, p)
		}
	}
}

func TestПриёмЖивПокаОтчитываетсяХотяБыКтоТо(t *testing.T) {
	// Первая версия этой проверки брала самую давно отчитывавшуюся машину и
	// на живой службе объявляла «ни одного отчёта четыре дня» в тот момент,
	// когда отчёты шли каждые несколько минут.
	//
	// Половина парка молчит неделю, но кто-то отчитался минуту назад: приём
	// работает, и говорить надо только о молчащих.
	src := &источник{stats: storage.Stats{
		Hosts:        10,
		HostsSilent:  5,
		OldestReport: 7 * 24 * time.Hour,
		LastReport:   time.Minute,
	}}

	r, _ := Check(t.Context(), src, сейчас)

	for _, p := range r.Problems {
		if p.Code == "отчёты-не-приходят" {
			t.Errorf("приём объявлен сломанным, хотя отчёт был минуту назад: %s", p.Summary)
		}
	}
	if len(r.Problems) != 1 || r.Problems[0].Code != "парк-замолчал" {
		t.Errorf("ожидалась одна неполадка о молчащих, получено: %+v", r.Problems)
	}
}
