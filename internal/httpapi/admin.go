package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	// Псевдоним: в этом пакете уже есть обработчик с именем commands.
	cmdqueue "github.com/broton07/inventory_service/internal/commands"
	// Псевдоним: в этом пакете уже есть обработчик с именем health.
	srvhealth "github.com/broton07/inventory_service/internal/health"
	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// Административный вход: постановка заданий не агентом, а другой системой.
//
// Нужен ровно для одного — кнопки «Обновить сейчас» в чужом интерфейсе.
// Человек, который смотрит на карточку компьютера в учётной системе, хочет
// свежие сведения сейчас, а не через четыре часа; учётная система просит
// службу, служба кладёт задание в очередь, агент забирает его при ближайшем
// опросе.
//
// Инвариант I1 при этом цел: служба по-прежнему не знает, кто её просит. Она
// знает, что просящий предъявил ключ, и всё.
//
// Ключ проверяется постоянным по времени сравнением. Разница невелика, но
// сравнение строк на равенство завершается на первом несовпавшем байте, и по
// времени ответа ключ подбирается посимвольно.

// Administrator — то, что административные маршруты знают об очереди заданий.
type Administrator interface {
	Create(
		ctx context.Context, agentUUID uuid.UUID, hostID *uuid.UUID,
		kind contract.CommandKind, params map[string]any, createdBy string,
	) (uuid.UUID, error)
	AgentOfHost(ctx context.Context, hostID uuid.UUID) (uuid.UUID, error)
	Facts(ctx context.Context, hostID uuid.UUID) (storage.HostFacts, error)
}

// Doctor — то, что умеет рассказать о состоянии самой службы.
type Doctor interface {
	Snapshot(ctx context.Context) (storage.Stats, error)
}

// requireAdmin пропускает дальше только с административным ключом.
func requireAdmin(token string, log *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	expected := []byte(token)

	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := bearer(r)
		if !ok || subtle.ConstantTimeCompare([]byte(key), expected) != 1 {
			log.Warn("отклонён административный запрос", "ip", clientIP(r), "path", r.URL.Path)
			unauthorized(w, "нужен административный ключ")
			return
		}
		next(w, r)
	}
}

// adminCollect — обработчик POST /v1/admin/hosts/{id}/collect.
func adminCollect(admin Administrator, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hostID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "неверный идентификатор машины")
			return
		}

		var body struct {
			By string `json:"by"`
		}
		// Тело необязательно: единственное поле — кто просит, и оно попадает
		// только в журнал.
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)

		agentUUID, err := admin.AgentOfHost(r.Context(), hostID)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "у машины нет действующего агента")
			return
		}
		if err != nil {
			log.Error("поиск агента машины не удался", "err", err, "host", hostID)
			writeError(w, http.StatusInternalServerError, "не удалось найти агента машины")
			return
		}

		by := body.By
		if by == "" {
			by = "внешняя система"
		}

		id, err := admin.Create(
			r.Context(), agentUUID, &hostID, contract.CommandCollectNow, nil, by)
		if errors.Is(err, cmdqueue.ErrUnknownKind) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err != nil {
			log.Error("постановка задания не удалась", "err", err, "host", hostID)
			writeError(w, http.StatusInternalServerError, "не удалось поставить задание")
			return
		}

		log.Info("поставлено задание извне", "command", id, "host", hostID, "by", by)

		writeJSON(w, http.StatusAccepted, map[string]any{
			"command_id": id,
			"note":       "агент заберёт задание при ближайшем опросе",
		})
	}
}

// adminFacts — обработчик GET /v1/admin/hosts/{id}.
//
// Отвечает на единственный вопрос, на который лента ответить не может:
// «машина отчиталась или молчит?». В уплотняемый топик состояние уходит
// только при расхождении, поэтому у потребителя дата последнего отчёта
// застывает на дне последнего изменения — и машина, которая исправно
// отчитывается каждые четыре часа и ничем не меняется, выглядит в учёте
// брошенной с весны.
//
// Спрашивают по требованию человека, а не расписанием: это один запрос на
// открытую карточку, а не пятьсот сообщений в час ради состояния, которое не
// менялось.
func adminFacts(admin Administrator, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hostID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "неверный идентификатор машины")
			return
		}

		facts, err := admin.Facts(r.Context(), hostID)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "машина не найдена")
			return
		}
		if err != nil {
			log.Error("сводка по машине не собралась", "err", err, "host", hostID)
			writeError(w, http.StatusInternalServerError, "не удалось собрать сводку")
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"host_id":           facts.ID,
			"hostname":          facts.Hostname,
			"first_seen_at":     facts.FirstSeenAt,
			"last_report_at":    facts.LastReportAt,
			"last_collected_at": facts.LastCollectedAt,
			"last_change_at":    facts.LastChangeAt,
			"silent_since":      facts.SilentSince,
			"retired_at":        facts.RetiredAt,
		})
	}
}

// adminHealth — обработчик GET /v1/admin/health.
//
// Состояние службы и список неполадок словами. Нужен потому, что мониторинг
// разворачивают не везде и не сразу, а человек, открывший экран
// инвентаризации и увидевший устаревшие данные, должен узнать причину там же.
// Показатели в /metrics отвечают на тот же вопрос, но их читает Prometheus, а
// не человек.
func adminHealth(doctor Doctor, log *slog.Logger, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report, err := srvhealth.Check(r.Context(), doctor, now().UTC())
		if err != nil {
			log.Error("состояние службы не собралось", "err", err)
			writeError(w, http.StatusInternalServerError, "не удалось собрать состояние службы")
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}
