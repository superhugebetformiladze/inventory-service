package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/broton07/inventory_service/internal/storage"
)

type списокАдресов struct {
	источник string
	адреса   []storage.NetworkTarget
}

func (s *списокАдресов) ReplaceNetworkTargets(
	_ context.Context, source string, targets []storage.NetworkTarget,
) (storage.TargetsChange, error) {
	s.источник, s.адреса = source, targets
	return storage.TargetsChange{Added: len(targets), Total: len(targets)}, nil
}

func (s *списокАдресов) ListNetworkTargets(context.Context) ([]storage.NetworkTargetRow, error) {
	out := make([]storage.NetworkTargetRow, 0, len(s.адреса))
	for _, a := range s.адреса {
		out = append(out, storage.NetworkTargetRow{IP: a.IP, Name: a.Name, Source: s.источник, LastError: "Timeout"})
	}
	return out, nil
}

type опрос struct{ толчков int }

func (o *опрос) Kick() bool              { o.толчков++; return true }
func (o *опрос) Interval() time.Duration { return 30 * time.Minute }

func запросАдм(t *testing.T, router http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestСписокОпросаОтВнешнейСистемы(t *testing.T) {
	store := &списокАдресов{}
	scanner := &опрос{}
	router := Router(Deps{Auth: &проверяющий{}, AdminToken: "секрет", NetTargets: store, Scanner: scanner})

	if rec := запросАдм(t, router, http.MethodPut, "/v1/admin/network/targets", "", `{}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("без ключа: %d", rec.Code)
	}

	rec := запросАдм(t, router, http.MethodPut, "/v1/admin/network/targets", "секрет",
		`{"source":"учёт","targets":[{"ip":" 192.168.8.10 ","name":"ядро"},{"ip":"192.168.4.172","name":"sw-412"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("замена списка: %d %s", rec.Code, rec.Body)
	}
	if store.источник != "учёт" || len(store.адреса) != 2 || store.адреса[0].IP != "192.168.8.10" {
		t.Errorf("записано: %q %+v", store.источник, store.адреса)
	}

	rec = запросАдм(t, router, http.MethodGet, "/v1/admin/network/targets", "секрет", "")
	var got struct {
		Scanning bool `json:"scanning"`
		Interval int  `json:"interval_seconds"`
		Targets  []struct {
			IP        string `json:"ip"`
			LastError string `json:"last_error"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Scanning || got.Interval != 1800 || len(got.Targets) != 2 || got.Targets[0].LastError != "Timeout" {
		t.Errorf("список: %+v", got)
	}

	if rec := запросАдм(t, router, http.MethodPost, "/v1/admin/network/poll", "секрет", ""); rec.Code != http.StatusAccepted || scanner.толчков != 1 {
		t.Errorf("опросить сейчас: %d, толчков %d", rec.Code, scanner.толчков)
	}
}

func TestСписокОпросаПроверяетсяНаВходе(t *testing.T) {
	router := Router(Deps{Auth: &проверяющий{}, AdminToken: "секрет", NetTargets: &списокАдресов{}})

	for name, body := range map[string]string{
		"без источника":  `{"targets":[]}`,
		"чужой config":   `{"source":"config","targets":[]}`,
		"не адрес":       `{"source":"учёт","targets":[{"ip":"sw-412"}]}`,
		"не разбирается": `{"source":`,
	} {
		if rec := запросАдм(t, router, http.MethodPut, "/v1/admin/network/targets", "секрет", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}

	// Опрос выключен (нет строки доступа) — список принимается, а просьба
	// опросить сейчас отклоняется словами, а не молчанием.
	rec := запросАдм(t, router, http.MethodPost, "/v1/admin/network/poll", "секрет", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "SNMP_COMMUNITY") {
		t.Errorf("опрос выключен: %d %s", rec.Code, rec.Body)
	}
}
