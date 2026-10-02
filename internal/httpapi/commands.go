package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// MaxCommandsPerPoll — сколько заданий отдавать за один опрос.
//
// Больше трёх агенту всё равно нечего делать одновременно, а очередь из сотни
// заданий, накопившаяся у машины из отпуска, не должна выливаться на неё
// разом.
const MaxCommandsPerPoll = 3

// Commander — то, что маршруты знают об очереди заданий.
type Commander interface {
	Take(ctx context.Context, agentUUID uuid.UUID, limit int) ([]contract.Command, error)
	Finish(ctx context.Context, id, agentUUID uuid.UUID, result contract.CommandResult) error
}

// commands — обработчик GET /v1/commands.
//
// Это самый частый запрос ко всей службе: пятьсот агентов раз в минуту, и
// почти всегда ответ пустой. Поэтому он не делает ничего, кроме одного запроса
// к базе по частичному индексу.
func commands(cmd Commander, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agent, ok := agentFrom(r.Context())
		if !ok {
			unauthorized(w, "ключ не признан")
			return
		}

		list, err := cmd.Take(r.Context(), agent.ID, MaxCommandsPerPoll)
		if err != nil {
			log.Error("выдача заданий не удалась", "err", err, "agent_id", agent.AgentID)
			writeError(w, http.StatusInternalServerError, "не удалось получить задания")
			return
		}

		if len(list) > 0 {
			log.Info("выданы задания", "agent_id", agent.AgentID, "count", len(list))
		}

		// Пустой список, а не null: агенту проще перебрать пустой массив, чем
		// каждый раз проверять на отсутствие.
		if list == nil {
			list = []contract.Command{}
		}

		writeJSON(w, http.StatusOK, map[string]any{"commands": list})
	}
}

// commandResult — обработчик POST /v1/commands/{id}/result.
func commandResult(cmd Commander, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agent, ok := agentFrom(r.Context())
		if !ok {
			unauthorized(w, "ключ не признан")
			return
		}

		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "неверный идентификатор задания")
			return
		}

		var result contract.CommandResult
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&result); err != nil {
			writeError(w, http.StatusBadRequest, "результат не разбирается как JSON")
			return
		}

		err = cmd.Finish(r.Context(), id, agent.ID, result)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			// Задание чужое, уже закрыто или его нет вовсе. Для агента это
			// один и тот же случай: делать больше нечего.
			writeError(w, http.StatusNotFound, "задание не найдено или уже закрыто")
			return
		case err != nil:
			log.Error("запись результата не удалась", "err", err, "command", id)
			writeError(w, http.StatusInternalServerError, "не удалось записать результат")
			return
		}

		if !result.OK {
			log.Warn("агент не выполнил задание",
				"command", id, "agent_id", agent.AgentID, "error", result.Error)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// CommandTTL — сколько задание ждёт агента, прежде чем протухнуть.
//
// Без срока «собрать сейчас», поставленное в понедельник ноутбуку, уехавшему в
// отпуск, сработает через три недели — когда никому уже не нужно и никто не
// помнит, зачем просили.
const CommandTTL = 24 * time.Hour
