package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/agents"
	"github.com/broton07/inventory_service/internal/ingest"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

var момент = time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)

// проверяющий изображает службу ключей.
type проверяющий struct {
	ключ    string
	агент   storage.Agent
	seenРаз int
}

func (п *проверяющий) Enroll(_ context.Context, req agents.EnrollRequest) (agents.EnrollResult, error) {
	if req.EnrollmentToken != "enr_действующий-токен-раскатки" {
		return agents.EnrollResult{}, agents.ErrDenied
	}
	if req.AgentID == "" {
		return agents.EnrollResult{}, agents.ErrBadRequest
	}
	return agents.EnrollResult{
		Agent: storage.Agent{ID: uuid.New(), AgentID: req.AgentID},
		Key:   "inv_свежевыданный-ключ-машины",
	}, nil
}

func (п *проверяющий) Authenticate(_ context.Context, key string) (storage.Agent, error) {
	if key != п.ключ {
		return storage.Agent{}, agents.ErrDenied
	}
	return п.агент, nil
}

func (п *проверяющий) Seen(context.Context, storage.Agent, string, string) error {
	п.seenРаз++
	return nil
}

// принимающий изображает приём отчётов и запоминает, что до него дошло.
type принимающий struct {
	принято contract.Snapshot
	вызовов int
}

func (п *принимающий) Accept(_ context.Context, snap contract.Snapshot, _ *uuid.UUID) (ingest.Result, error) {
	п.вызовов++
	п.принято = snap
	return ingest.Result{HostID: uuid.New(), SnapshotID: uuid.New()}, nil
}

func стенд(t *testing.T) (*http.ServeMux, *проверяющий, *принимающий) {
	t.Helper()

	auth := &проверяющий{
		ключ:  "inv_ключ-машины-один",
		агент: storage.Agent{ID: uuid.New(), AgentID: "машина-1"},
	}
	acceptor := &принимающий{}

	mux := Router(Deps{
		Auth:     auth,
		Acceptor: acceptor,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      func() time.Time { return момент },
	})
	return mux, auth, acceptor
}

func отчёт(agentID string) string {
	snap := contract.Snapshot{
		SchemaVersion: contract.SchemaVersion,
		AgentID:       agentID,
		CollectedAt:   момент.Add(-time.Minute),
		Origin:        contract.OriginNetwork,
		Machine:       contract.Machine{Hostname: "PC-01"},
	}
	body, _ := json.Marshal(snap)
	return string(body)
}

func запрос(t *testing.T, mux *http.ServeMux, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestОтчётБезКлючаОтклоняется(t *testing.T) {
	mux, _, acceptor := стенд(t)

	rec := запрос(t, mux, http.MethodPost, "/v1/reports", "", отчёт("машина-1"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ожидался 401, получен %d", rec.Code)
	}
	if acceptor.вызовов != 0 {
		t.Error("отчёт без ключа не должен доходить до приёма")
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("в отказе нет схемы аутентификации — агенту придётся гадать")
	}
}

func TestОтчётСЧужимКлючомОтклоняется(t *testing.T) {
	mux, _, acceptor := стенд(t)

	rec := запрос(t, mux, http.MethodPost, "/v1/reports", "inv_чужой-ключ", отчёт("машина-1"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ожидался 401, получен %d", rec.Code)
	}
	if acceptor.вызовов != 0 {
		t.Error("отчёт с чужим ключом не должен доходить до приёма")
	}
}

func TestОтчётСДействующимКлючомПринимается(t *testing.T) {
	mux, auth, acceptor := стенд(t)

	rec := запрос(t, mux, http.MethodPost, "/v1/reports", auth.ключ, отчёт("машина-1"))

	if rec.Code != http.StatusCreated {
		t.Fatalf("ожидался 201, получен %d: %s", rec.Code, rec.Body.String())
	}
	if acceptor.вызовов != 1 {
		t.Fatalf("приём вызван %d раз", acceptor.вызовов)
	}
	if auth.seenРаз != 1 {
		t.Error("отметка «на связи» не обновлена")
	}
}

func TestОтметкаАгентаБерётсяИзКлючаАНеИзТела(t *testing.T) {
	// Иначе машина с действующим ключом присылает отчёт от имени соседа и
	// переписывает его карточку.
	mux, auth, acceptor := стенд(t)

	rec := запрос(t, mux, http.MethodPost, "/v1/reports", auth.ключ, отчёт("чужая-машина"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ожидался 400, получен %d", rec.Code)
	}
	if acceptor.вызовов != 0 {
		t.Error("отчёт с чужой отметкой не должен доходить до приёма")
	}
}

func TestПустаяОтметкаВТелеЗаполняетсяИзКлюча(t *testing.T) {
	// Агенту незачем повторять то, что и так известно из ключа.
	mux, auth, acceptor := стенд(t)

	rec := запрос(t, mux, http.MethodPost, "/v1/reports", auth.ключ, отчёт(""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("ожидался 201, получен %d: %s", rec.Code, rec.Body.String())
	}
	if acceptor.принято.AgentID != "машина-1" {
		t.Errorf("отметка не подставлена из ключа: %q", acceptor.принято.AgentID)
	}
}

func TestРегистрацияВыдаётКлючОдинРаз(t *testing.T) {
	mux, _, _ := стенд(t)

	rec := запрос(t, mux, http.MethodPost, "/v1/enroll", "",
		`{"enrollment_token":"enr_действующий-токен-раскатки","agent_id":"машина-2"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("ожидался 201, получен %d: %s", rec.Code, rec.Body.String())
	}

	var answer struct {
		AgentKey string `json:"agent_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("ответ не разбирается: %v", err)
	}
	if answer.AgentKey == "" {
		t.Error("ключ не выдан")
	}
}

func TestРегистрацияСЧужимТокеномНеРаскрываетПричину(t *testing.T) {
	mux, _, _ := стенд(t)

	rec := запрос(t, mux, http.MethodPost, "/v1/enroll", "",
		`{"enrollment_token":"enr_чужой-токен","agent_id":"машина-2"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ожидался 401, получен %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "срок") || strings.Contains(rec.Body.String(), "исчерпан") {
		t.Errorf("ответ раскрывает причину отказа: %s", rec.Body.String())
	}
}

func TestБезПроверкиКлючейМаршрутовАгентаНет(t *testing.T) {
	// Открытый приём отчётов — это возможность записать в учёт что угодно от
	// имени любой машины. Такой режим не должен получаться случайно, из-за
	// недособранной зависимости.
	mux := Router(Deps{
		Acceptor: &принимающий{},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	for _, path := range []string{"/v1/reports", "/v1/enroll"} {
		rec := запрос(t, mux, http.MethodPost, path, "", "{}")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s без проверки ключей отвечает %d, ожидался 404", path, rec.Code)
		}
	}
}

func TestОграничительЧастотыРегистрации(t *testing.T) {
	// Ограничитель защищает не токен, а саму службу: настоящий предел
	// злоупотребления — счётчик использований токена. Здесь проверяется, что
	// механизм работает; предел задан явно, а не взят из умолчания.
	mux := Router(Deps{
		Auth:        &проверяющий{},
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:         func() time.Time { return момент },
		EnrollBurst: 3,
	})

	var отказов int
	for range 20 {
		rec := запрос(t, mux, http.MethodPost, "/v1/enroll", "",
			`{"enrollment_token":"enr_подбираемый-токен","agent_id":"м"}`)
		if rec.Code == http.StatusTooManyRequests {
			отказов++
		}
	}

	if отказов == 0 {
		t.Fatal("ограничитель не сработал ни разу за двадцать попыток")
	}
}

func TestУмолчаниеОграничителяНеЛомаетРаскатку(t *testing.T) {
	// Найдено нагрузочным прогоном: при пределе в десять регистраций в минуту
	// раскатка на тысячу машин дала десять зарегистрированных и девятьсот
	// девяносто отказов.
	//
	// Групповая политика поднимает агента на сотнях машин за минуты, и если
	// служба стоит за обратным прокси, все они приходят с одного адреса.
	mux, _, _ := стенд(t)

	var отказов int
	for range 50 {
		rec := запрос(t, mux, http.MethodPost, "/v1/enroll", "",
			`{"enrollment_token":"enr_токен-раскатки-длинный","agent_id":"м"}`)
		if rec.Code == http.StatusTooManyRequests {
			отказов++
		}
	}

	if отказов > 0 {
		t.Fatalf("из пятидесяти регистраций подряд отклонено %d: раскатка сломается", отказов)
	}
}
