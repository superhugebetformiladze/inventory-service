package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/ingest"
	"github.com/broton07/inventory_service/internal/metrics"
	"github.com/broton07/inventory_service/pkg/contract"
)

// MaxReportBytes — предел размера отчёта.
//
// Снимок машины со списком установленных программ — это десятки килобайт.
// Мегабайт с запасом покрывает даже машину с сотнями программ, но не даёт
// одним запросом занять всю память службы.
const MaxReportBytes = 1 << 20

// Acceptor — то, что маршрут делает с отчётом.
type Acceptor interface {
	Accept(ctx context.Context, snap contract.Snapshot, agentUUID *uuid.UUID) (ingest.Result, error)
}

// reports — обработчик POST /v1/reports. Доступен только с действующим ключом.
func reports(acceptor Acceptor, auth Authenticator, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Замер здесь, а не в приёме: время, которое видит агент, включает и
		// разбор запроса, и запись в базу. Мерить только середину значит
		// однажды искать потерянные секунды не там.
		начало := time.Now()
		исход := "rejected"
		defer func() {
			metrics.ReportsTotal.WithLabelValues(исход).Inc()
			metrics.ReportDuration.Observe(time.Since(начало).Seconds())
		}()

		agent, ok := agentFrom(r.Context())
		if !ok {
			// Сюда попасть нельзя: маршрут завёрнут в проверку ключа. Но
			// если однажды кто-то перевесит его мимо неё, отказать лучше,
			// чем принять отчёт неизвестно от кого.
			unauthorized(w, "ключ не признан")
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxReportBytes))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge,
				"отчёт больше допустимого размера")
			return
		}

		var snap contract.Snapshot
		// Именно обычный Unmarshal, без запрета неизвестных полей: агент новой
		// версии присылает поля, о которых служба ещё не знает, и падать на
		// них означало бы останавливать сбор со всего парка на время
		// раскатки обновления (инвариант I5).
		if err := json.Unmarshal(body, &snap); err != nil {
			writeError(w, http.StatusBadRequest, "отчёт не разбирается как JSON")
			return
		}

		// Отметка агента берётся из КЛЮЧА, а не из тела отчёта.
		//
		// Иначе машина с действующим ключом присылает отчёт от имени соседа и
		// переписывает его карточку. Тело при этом не игнорируется молча:
		// несовпадение — это либо ошибка в агенте, либо попытка подмены, и
		// оба случая надо видеть, а не сглаживать.
		if snap.AgentID != "" && snap.AgentID != agent.AgentID {
			log.Warn("отчёт с чужой отметкой агента",
				"key_agent_id", agent.AgentID, "body_agent_id", snap.AgentID, "ip", clientIP(r))
			writeError(w, http.StatusBadRequest,
				"отметка агента в отчёте не совпадает с той, которой выдан ключ")
			return
		}
		snap.AgentID = agent.AgentID

		result, err := acceptor.Accept(r.Context(), snap, &agent.ID)
		switch {
		case errors.Is(err, ingest.ErrInvalid):
			// Причина отдаётся агенту целиком: он единственный, кто может её
			// исправить, а разбираться в «400 Bad Request» без подробностей
			// придётся человеку с журналом службы в руках.
			writeError(w, http.StatusBadRequest, err.Error())
			return
		case err != nil:
			log.Error("приём отчёта не удался", "err", err, "agent_id", snap.AgentID)
			writeError(w, http.StatusInternalServerError, "не удалось принять отчёт")
			return
		}

		// Отметка «на связи» двигается и на повторе: агент выходил на связь —
		// это факт, независимый от того, изменилось ли что-нибудь. Неудача
		// здесь не отменяет принятого отчёта, поэтому только в журнал.
		if err := auth.Seen(r.Context(), agent, snap.AgentVersion, snap.Machine.Hostname); err != nil {
			log.Warn("не удалось обновить отметку агента", "err", err, "agent_id", agent.AgentID)
		}

		// Отчёт принят, но машину опознать не удалось. Для агента это успех:
		// его дело — доставить отчёт, а не разбираться, чей он. Ответить
		// ошибкой значило бы заставить агента повторять вечно то, что чинится
		// только человеком.
		if result.Unresolved {
			log.Warn("машина не опознана, отчёт отложен",
				"agent_id", snap.AgentID,
				"hostname", snap.Machine.Hostname,
				"reason", result.Reason)

			исход = "unresolved"
			writeJSON(w, http.StatusAccepted, map[string]any{
				"unresolved": true,
				"reason":     result.Reason,
			})
			return
		}

		log.Info("отчёт принят",
			"host_id", result.HostID,
			"agent_id", snap.AgentID,
			"hostname", snap.Machine.Hostname,
			"duplicate", result.Duplicate,
			"first_seen", result.FirstSeen)

		// Повтор — не ошибка, а нормальный исход: агент переотправил то, что
		// уже дошло. Отдельный код ответа нужен, чтобы агент видел разницу и
		// не считал, что его первая попытка пропала.
		status := http.StatusCreated
		исход = "accepted"
		if result.Duplicate {
			status = http.StatusOK
			исход = "duplicate"
		}

		writeJSON(w, status, map[string]any{
			"host_id":     result.HostID,
			"snapshot_id": result.SnapshotID,
			"duplicate":   result.Duplicate,
			"first_seen":  result.FirstSeen,
		})
	}
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
