package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

func TestHealthОтвечаетБезЗависимостей(t *testing.T) {
	rec := httptest.NewRecorder()
	Router(Deps{}).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d", rec.Code)
	}
}

func TestHealthНеЗависитОтУпавшихПроверок(t *testing.T) {
	// Смысл разделения /health и /ready: упавшая база делает службу не
	// готовой, но не мёртвой. Иначе перезапуск базы перезапускает и службу.
	checks := []Check{{Name: "postgres", Func: func(context.Context) error {
		return errors.New("соединение отклонено")
	}}}

	rec := httptest.NewRecorder()
	Router(Deps{Checks: checks}).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("/health должен отвечать 200 при упавшей зависимости, получен %d", rec.Code)
	}
}

func TestReadyОтдаёт503КогдаЗависимостьНедоступна(t *testing.T) {
	checks := []Check{
		{Name: "postgres", Func: func(context.Context) error { return nil }},
		{Name: "kafka", Func: func(context.Context) error { return errors.New("соединение отклонено") }},
	}

	rec := httptest.NewRecorder()
	Router(Deps{Checks: checks}).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ожидался 503, получен %d", rec.Code)
	}

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("ответ не разобрался: %v", err)
	}
	if body.Checks["postgres"] != "ok" {
		t.Errorf("исправная проверка должна остаться ok, получено %q", body.Checks["postgres"])
	}
	if body.Checks["kafka"] == "ok" {
		t.Error("упавшая проверка не должна быть ok")
	}
}

func TestReadyОтдаёт200КогдаВсёНаМесте(t *testing.T) {
	checks := []Check{{Name: "postgres", Func: func(context.Context) error { return nil }}}

	rec := httptest.NewRecorder()
	Router(Deps{Checks: checks}).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d", rec.Code)
	}
}

// ============================================================
// Административный вход
// ============================================================

type администратор struct {
	agent   uuid.UUID
	создано int
	by      string
}

func (а *администратор) Create(
	_ context.Context, _ uuid.UUID, _ *uuid.UUID,
	_ contract.CommandKind, _ map[string]any, createdBy string,
) (uuid.UUID, error) {
	а.создано++
	а.by = createdBy
	return uuid.New(), nil
}

func (а *администратор) AgentOfHost(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	if а.agent == uuid.Nil {
		return uuid.Nil, storage.ErrNotFound
	}
	return а.agent, nil
}

func (а *администратор) Facts(_ context.Context, id uuid.UUID) (storage.HostFacts, error) {
	if а.agent == uuid.Nil {
		return storage.HostFacts{}, storage.ErrNotFound
	}
	return storage.HostFacts{ID: id, Hostname: "PC-TEST", LastReportAt: time.Now().UTC()}, nil
}

func TestБезКлючаАдминистративныхМаршрутовНет(t *testing.T) {
	// Открытый вход, через который можно поставить задание любой машине
	// парка, не должен получаться из-за незаданной переменной.
	admin := &администратор{agent: uuid.New()}
	router := Router(Deps{Auth: &проверяющий{}, Admin: admin, AdminToken: ""})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/admin/hosts/"+uuid.NewString()+"/collect", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("маршрут отвечает %d, а его не должно быть вовсе", rec.Code)
	}
	if admin.создано != 0 {
		t.Error("задание поставлено без ключа")
	}
}

func TestАдминистративныйЗапросБезКлючаОтклоняется(t *testing.T) {
	admin := &администратор{agent: uuid.New()}
	router := Router(Deps{Auth: &проверяющий{}, Admin: admin, AdminToken: "секрет"})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/admin/hosts/"+uuid.NewString()+"/collect", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("получено %d, ожидался отказ", rec.Code)
	}
	if admin.создано != 0 {
		t.Error("задание поставлено без ключа")
	}
}

func TestАдминистративныйЗапросСКлючомСтавитЗадание(t *testing.T) {
	admin := &администратор{agent: uuid.New()}
	router := Router(Deps{Auth: &проверяющий{}, Admin: admin, AdminToken: "секрет"})

	body := strings.NewReader(`{"by":"учётная система"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/admin/hosts/"+uuid.NewString()+"/collect", body)
	req.Header.Set("Authorization", "Bearer секрет")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("получено %d, ожидалось 202: %s", rec.Code, rec.Body.String())
	}
	if admin.создано != 1 {
		t.Fatalf("поставлено заданий: %d", admin.создано)
	}
	if admin.by != "учётная система" {
		t.Errorf("в задании записан проситель %q", admin.by)
	}
}

func TestЗаданиеМашинеБезАгентаНеСтавится(t *testing.T) {
	// Машина есть в учёте, но агента на ней нет: её сведения приехали файлом
	// или агента отозвали. Просить нечего.
	admin := &администратор{}
	router := Router(Deps{Auth: &проверяющий{}, Admin: admin, AdminToken: "секрет"})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/admin/hosts/"+uuid.NewString()+"/collect", nil)
	req.Header.Set("Authorization", "Bearer секрет")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("получено %d, ожидалось 404", rec.Code)
	}
}

func TestСводкаПоМашинеТребуетКлюч(t *testing.T) {
	admin := &администратор{agent: uuid.New()}
	router := Router(Deps{Auth: &проверяющий{}, Admin: admin, AdminToken: "секрет"})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/v1/admin/hosts/"+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("получено %d, ожидался отказ", rec.Code)
	}
}

func TestСводкаПоМашинеОтдаётВремяОтчёта(t *testing.T) {
	// Ради этого маршрут и заведён: в ленту состояние уходит только при
	// расхождении, и без прямого вопроса потребитель не отличит машину,
	// которая исправно отчитывается и не меняется, от брошенной.
	admin := &администратор{agent: uuid.New()}
	router := Router(Deps{Auth: &проверяющий{}, Admin: admin, AdminToken: "секрет"})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/v1/admin/hosts/"+uuid.NewString(), nil)
	req.Header.Set("Authorization", "Bearer секрет")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("получено %d, ожидалось 200: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Hostname     string    `json:"hostname"`
		LastReportAt time.Time `json:"last_report_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if body.Hostname != "PC-TEST" {
		t.Errorf("имя машины %q", body.Hostname)
	}
	if body.LastReportAt.IsZero() {
		t.Error("время последнего отчёта не отдано — ради него весь маршрут")
	}
}
