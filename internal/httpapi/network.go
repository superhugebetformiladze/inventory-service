package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/broton07/inventory_service/internal/storage"
)

// Список опроса сетевых устройств — административный вход.
//
// Внешняя система (учёт) знает, какие коммутаторы у неё заведены и с какими
// адресами, и присылает их сюда целиком. Служба по-прежнему не знает, кто
// это (I1): source — просто метка, по которой заменяются «его» строки.

// NetworkTargets — то, что маршрутам нужно от базы.
type NetworkTargets interface {
	ReplaceNetworkTargets(ctx context.Context, source string, targets []storage.NetworkTarget) (storage.TargetsChange, error)
	ListNetworkTargets(ctx context.Context) ([]storage.NetworkTargetRow, error)
}

// Scanner — периодический опрос, если он включён.
type Scanner interface {
	Kick() bool
	Interval() time.Duration
}

// maxTargets — предел списка за один запрос. Сеть предприятия — сотни
// коммутаторов; десятки тысяч — это ошибка вызывающего, а не сеть.
const maxTargets = 5000

type targetJSON struct {
	IP            string     `json:"ip"`
	Name          string     `json:"name"`
	Source        string     `json:"source,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastOKAt      *time.Time `json:"last_ok_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	DeviceID      string     `json:"device_id,omitempty"`
	SilentSince   *time.Time `json:"silent_since,omitempty"`
}

// adminListTargets — GET /v1/admin/network/targets.
func adminListTargets(store NetworkTargets, scanner Scanner, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.ListNetworkTargets(r.Context())
		if err != nil {
			log.Error("список адресов опроса не прочитан", "err", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать список опроса")
			return
		}
		out := make([]targetJSON, 0, len(rows))
		for _, row := range rows {
			out = append(out, targetJSON{
				IP: row.IP, Name: row.Name, Source: row.Source,
				LastAttemptAt: row.LastAttemptAt, LastOKAt: row.LastOKAt,
				LastError: row.LastError, DeviceID: row.DeviceID, SilentSince: row.SilentSince,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"scanning":         scanner != nil,
			"interval_seconds": interval(scanner),
			"targets":          out,
		})
	}
}

// adminReplaceTargets — PUT /v1/admin/network/targets.
func adminReplaceTargets(store NetworkTargets, scanner Scanner, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Source  string       `json:"source"`
			Targets []targetJSON `json:"targets"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "тело запроса не разбирается")
			return
		}
		body.Source = strings.TrimSpace(body.Source)
		if body.Source == "" || len(body.Source) > 64 {
			writeError(w, http.StatusBadRequest, "нужен source — кто присылает список (до 64 символов)")
			return
		}
		// «config» — строки из настроек службы; чужой список их не заменяет.
		if body.Source == "config" {
			writeError(w, http.StatusBadRequest, "source «config» занят настройками службы")
			return
		}
		if len(body.Targets) > maxTargets {
			writeError(w, http.StatusBadRequest, "слишком длинный список")
			return
		}

		targets := make([]storage.NetworkTarget, 0, len(body.Targets))
		for _, t := range body.Targets {
			addr, err := netip.ParseAddr(strings.TrimSpace(t.IP))
			if err != nil {
				writeError(w, http.StatusBadRequest, "неверный адрес: "+t.IP)
				return
			}
			name := strings.TrimSpace(t.Name)
			if len([]rune(name)) > 200 {
				name = string([]rune(name)[:200])
			}
			targets = append(targets, storage.NetworkTarget{IP: addr.String(), Name: name})
		}

		change, err := store.ReplaceNetworkTargets(r.Context(), body.Source, targets)
		if err != nil {
			log.Error("список опроса не записан", "err", err, "source", body.Source)
			writeError(w, http.StatusInternalServerError, "не удалось записать список опроса")
			return
		}
		log.Info("список опроса заменён", "source", body.Source,
			"added", change.Added, "removed", change.Removed, "total", change.Total)

		writeJSON(w, http.StatusOK, map[string]any{
			"added": change.Added, "removed": change.Removed, "total": change.Total,
			"scanning": scanner != nil,
		})
	}
}

// adminPollNow — POST /v1/admin/network/poll.
func adminPollNow(scanner Scanner, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if scanner == nil {
			writeError(w, http.StatusConflict, "опрос выключен: в службе не задан SNMP_COMMUNITY")
			return
		}
		queued := scanner.Kick()
		log.Info("запрошен внеочередной опрос", "queued", queued)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"queued": queued,
			"note":   "проход начнётся после текущего; итог — в списке опроса",
		})
	}
}

func interval(s Scanner) int {
	if s == nil {
		return 0
	}
	return int(s.Interval().Seconds())
}
