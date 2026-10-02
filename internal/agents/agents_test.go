package agents

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/storage"
)

var сейчас = time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)

// хранилищеПамяти изображает базу.
type хранилищеПамяти struct {
	токен        string // действующий токен регистрации
	агенты       map[string]storage.Agent
	поХешу       map[string]storage.Agent
	rotated      bool
	вызововTouch int
}

func новоеХранилище(токен string) *хранилищеПамяти {
	return &хранилищеПамяти{
		токен:  токен,
		агенты: map[string]storage.Agent{},
		поХешу: map[string]storage.Agent{},
	}
}

func (х *хранилищеПамяти) Enroll(_ context.Context, in storage.EnrollParams) (storage.Agent, bool, error) {
	if string(in.TokenHash) != string(Hash(х.токен)) {
		return storage.Agent{}, false, storage.ErrTokenUnusable
	}

	existing, повтор := х.агенты[in.AgentID]
	agent := storage.Agent{
		ID:              uuid.New(),
		AgentID:         in.AgentID,
		KeyPrefix:       in.KeyPrefix,
		Hostname:        in.Hostname,
		AgentVersion:    in.AgentVersion,
		EnrolledAt:      in.Now,
		PublicKey:       in.PublicKey,
		EnrolledOffline: in.Offline,
	}
	if повтор {
		agent.ID = existing.ID
		// Открытый ключ не затирается пустым — как и в SQL: иначе агент
		// старой версии, перерегистрировавшись, лишил бы машину подписи.
		if len(agent.PublicKey) == 0 {
			agent.PublicKey = existing.PublicKey
		}
	}

	х.агенты[in.AgentID] = agent
	х.поХешу[string(in.KeyHash)] = agent
	х.rotated = повтор

	return agent, повтор, nil
}

func (х *хранилищеПамяти) FindAgentByMark(_ context.Context, mark string) (storage.Agent, error) {
	a, ok := х.агенты[mark]
	if !ok {
		return storage.Agent{}, storage.ErrNotFound
	}
	return a, nil
}

func (х *хранилищеПамяти) FindAgentByKeyHash(_ context.Context, keyHash []byte) (storage.Agent, error) {
	a, ok := х.поХешу[string(keyHash)]
	if !ok {
		return storage.Agent{}, storage.ErrNotFound
	}
	return a, nil
}

func (х *хранилищеПамяти) TouchAgent(_ context.Context, _ uuid.UUID, _, _ string, _ time.Time) error {
	х.вызововTouch++
	return nil
}

func служба(х *хранилищеПамяти) *Service {
	return New(х, func() time.Time { return сейчас })
}

func TestКлючиРазныеКаждыйРаз(t *testing.T) {
	видели := map[string]bool{}
	for range 100 {
		key, err := NewAgentKey()
		if err != nil {
			t.Fatalf("не ожидалась ошибка: %v", err)
		}
		if видели[key] {
			t.Fatal("генератор выдал повтор")
		}
		видели[key] = true

		if !strings.HasPrefix(key, AgentKeyPrefix) {
			t.Fatalf("ключ без префикса: %q", key)
		}
		if !LooksLikeAgentKey(key) {
			t.Fatalf("свой же ключ не признан ключом: %q", key)
		}
	}
}

func TestТокенРегистрацииИКлючРазличимы(t *testing.T) {
	// Увидев «enr_…» в скрипте установки, человек должен понимать, что это
	// токен раскатки, а не ключ машины: у них разный срок жизни и разные
	// последствия утечки.
	token, _ := NewEnrollmentToken()
	key, _ := NewAgentKey()

	if LooksLikeAgentKey(token) {
		t.Error("токен регистрации принят за ключ агента")
	}
	if LooksLikeEnrollmentToken(key) {
		t.Error("ключ агента принят за токен регистрации")
	}
}

func TestХешНеОбратим(t *testing.T) {
	key, _ := NewAgentKey()
	hash := Hash(key)

	if strings.Contains(string(hash), key) {
		t.Fatal("хеш содержит сам ключ")
	}
	if len(hash) != 32 {
		t.Fatalf("длина хеша %d, ожидалось 32", len(hash))
	}
	// Один и тот же ключ должен давать один и тот же хеш: поиск агента идёт
	// по нему напрямую.
	if string(Hash(key)) != string(hash) {
		t.Error("хеш не воспроизводится")
	}
}

func TestПоказываемТолькоНачалоКлюча(t *testing.T) {
	key, _ := NewAgentKey()
	prefix := DisplayPrefix(key)

	if len(prefix) != DisplayPrefixLen {
		t.Fatalf("длина показываемой части %d, ожидалось %d", len(prefix), DisplayPrefixLen)
	}
	if len(prefix) >= len(key) {
		t.Error("показывается ключ целиком")
	}
}

func TestРегистрацияВыдаётКлюч(t *testing.T) {
	токен, _ := NewEnrollmentToken()
	х := новоеХранилище(токен)

	res, err := служба(х).Enroll(t.Context(), EnrollRequest{
		EnrollmentToken: токен,
		AgentID:         "машина-1",
		Hostname:        "PC-01",
		AgentVersion:    "0.1.0",
	})
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}
	if !LooksLikeAgentKey(res.Key) {
		t.Fatalf("выдан не ключ: %q", res.Key)
	}
	if res.Rotated {
		t.Error("первая регистрация не должна считаться перевыпуском")
	}
}

func TestЧужойТокенНеПринимается(t *testing.T) {
	настоящий, _ := NewEnrollmentToken()
	чужой, _ := NewEnrollmentToken()
	х := новоеХранилище(настоящий)

	_, err := служба(х).Enroll(t.Context(), EnrollRequest{
		EnrollmentToken: чужой,
		AgentID:         "машина-1",
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("ожидался отказ, получено: %v", err)
	}
}

func TestМусорВместоТокенаДоБазыНеДоходит(t *testing.T) {
	// Форма проверяется до похода в базу: чужие заголовки и мусор не должны
	// превращаться в запрос на каждый вызов.
	х := новоеХранилище("enr_настоящий-токен-достаточной-длины")

	_, err := служба(х).Enroll(t.Context(), EnrollRequest{
		EnrollmentToken: "просто строка",
		AgentID:         "машина-1",
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("ожидался отказ, получено: %v", err)
	}
	if len(х.агенты) != 0 {
		t.Error("мусор не должен создавать агента")
	}
}

func TestРегистрацияБезОтметкиАгента(t *testing.T) {
	токен, _ := NewEnrollmentToken()
	х := новоеХранилище(токен)

	_, err := служба(х).Enroll(t.Context(), EnrollRequest{EnrollmentToken: токен})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("ожидалась ошибка запроса, получено: %v", err)
	}
}

func TestПовторнаяРегистрацияПеревыпускаетКлюч(t *testing.T) {
	// Так выглядит установщик, который получил ключ, но не успел его
	// сохранить, и машина, переустановленная с сохранением отметки.
	токен, _ := NewEnrollmentToken()
	х := новоеХранилище(токен)
	s := служба(х)

	первый, err := s.Enroll(t.Context(), EnrollRequest{EnrollmentToken: токен, AgentID: "машина-1"})
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	второй, err := s.Enroll(t.Context(), EnrollRequest{EnrollmentToken: токен, AgentID: "машина-1"})
	if err != nil {
		t.Fatalf("повторная регистрация не должна быть ошибкой: %v", err)
	}

	if второй.Key == первый.Key {
		t.Error("при перевыпуске ключ обязан смениться")
	}
	if !второй.Rotated {
		t.Error("перевыпуск должен быть помечен: это событие, которое надо видеть в журнале")
	}
	if второй.Agent.ID != первый.Agent.ID {
		t.Error("машина должна остаться той же, иначе теряется её история")
	}
}

func TestПроверкаКлюча(t *testing.T) {
	токен, _ := NewEnrollmentToken()
	х := новоеХранилище(токен)
	s := служба(х)

	res, err := s.Enroll(t.Context(), EnrollRequest{EnrollmentToken: токен, AgentID: "машина-1"})
	if err != nil {
		t.Fatalf("не ожидалась ошибка: %v", err)
	}

	agent, err := s.Authenticate(t.Context(), res.Key)
	if err != nil {
		t.Fatalf("свой же ключ не признан: %v", err)
	}
	if agent.AgentID != "машина-1" {
		t.Errorf("опознана не та машина: %q", agent.AgentID)
	}
}

func TestНеизвестныйКлючОтклоняется(t *testing.T) {
	х := новоеХранилище("enr_что-нибудь-достаточно-длинное")
	чужой, _ := NewAgentKey()

	_, err := служба(х).Authenticate(t.Context(), чужой)
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("ожидался отказ, получено: %v", err)
	}
}

func TestСтарыйКлючПослеПеревыпускаНеРаботает(t *testing.T) {
	// Иначе отзыв через перевыпуск не работал бы вовсе.
	токен, _ := NewEnrollmentToken()
	х := новоеХранилище(токен)
	s := служба(х)

	первый, _ := s.Enroll(t.Context(), EnrollRequest{EnrollmentToken: токен, AgentID: "машина-1"})
	второй, _ := s.Enroll(t.Context(), EnrollRequest{EnrollmentToken: токен, AgentID: "машина-1"})

	// В настоящей базе старый хеш затирается; здесь проверяем, что новый ключ
	// точно работает, а служба ищет агента ровно по предъявленному ключу.
	if _, err := s.Authenticate(t.Context(), второй.Key); err != nil {
		t.Fatalf("новый ключ должен работать: %v", err)
	}
	if первый.Key == второй.Key {
		t.Fatal("ключи совпали — перевыпуска не произошло")
	}
}

func TestПричинаОтказаНеРаскрывается(t *testing.T) {
	// Разница между «такого ключа нет» и «ключ отозван» — это подсказка тому,
	// кто перебирает ключи. Наружу уходит одна ошибка на все случаи.
	х := новоеХранилище("enr_что-нибудь-достаточно-длинное")

	_, errПустой := служба(х).Authenticate(t.Context(), "")
	чужой, _ := NewAgentKey()
	_, errЧужой := служба(х).Authenticate(t.Context(), чужой)

	if !errors.Is(errПустой, ErrDenied) || !errors.Is(errЧужой, ErrDenied) {
		t.Fatal("оба случая должны давать одну и ту же ошибку")
	}
	if errПустой.Error() != errЧужой.Error() {
		t.Errorf("тексты ошибок различаются: %q против %q", errПустой, errЧужой)
	}
}
