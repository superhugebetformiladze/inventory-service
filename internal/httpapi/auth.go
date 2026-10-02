package httpapi

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/broton07/inventory_service/internal/agents"
	"github.com/broton07/inventory_service/internal/storage"
)

// Authenticator — то, что маршруты знают о проверке ключей.
type Authenticator interface {
	Enroll(ctx context.Context, req agents.EnrollRequest) (agents.EnrollResult, error)
	Authenticate(ctx context.Context, key string) (storage.Agent, error)
	Seen(ctx context.Context, agent storage.Agent, version, hostname string) error
}

type agentContextKey struct{}

// agentFrom достаёт проверенного агента из контекста запроса.
func agentFrom(ctx context.Context) (storage.Agent, bool) {
	a, ok := ctx.Value(agentContextKey{}).(storage.Agent)
	return a, ok
}

// requireAgent пропускает дальше только запросы с действующим ключом.
func requireAgent(auth Authenticator, log *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := bearer(r)
		if !ok {
			unauthorized(w, "нужен заголовок Authorization: Bearer <ключ агента>")
			return
		}

		agent, err := auth.Authenticate(r.Context(), key)
		switch {
		case errors.Is(err, agents.ErrDenied):
			// В журнал — начало ключа, не весь: журналы читают и копируют, а
			// ключ из журнала — это работающий ключ.
			log.Warn("отклонён неизвестный или отозванный ключ",
				"key_prefix", agents.DisplayPrefix(key), "ip", clientIP(r))
			unauthorized(w, "ключ не признан")
			return
		case err != nil:
			log.Error("проверка ключа не удалась", "err", err)
			writeError(w, http.StatusInternalServerError, "не удалось проверить ключ")
			return
		}

		ctx := context.WithValue(r.Context(), agentContextKey{}, agent)
		next(w, r.WithContext(ctx))
	}
}

func bearer(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	const scheme = "Bearer "
	if !strings.HasPrefix(header, scheme) {
		return "", false
	}
	key := strings.TrimSpace(header[len(scheme):])
	return key, key != ""
}

func unauthorized(w http.ResponseWriter, message string) {
	// Схема в заголовке — чтобы агент не гадал, чего от него хотят.
	w.Header().Set("WWW-Authenticate", `Bearer realm="inventory"`)
	writeError(w, http.StatusUnauthorized, message)
}

// enroll — обработчик POST /v1/enroll.
func enroll(auth Authenticator, limiter *throttle, log *slog.Logger, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !limiter.allow(clientIP(r), now()) {
			writeError(w, http.StatusTooManyRequests,
				"слишком много попыток регистрации, повторите позже")
			return
		}

		var req struct {
			EnrollmentToken string `json:"enrollment_token"`
			AgentID         string `json:"agent_id"`
			Hostname        string `json:"hostname"`
			AgentVersion    string `json:"agent_version"`

			// PublicKey — открытый ключ машины, base64. Агенты старых версий
			// его не присылают, и это не ошибка: без него машина просто не
			// сможет прислать отчёт файлом.
			PublicKey string `json:"public_key,omitempty"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "запрос не разбирается как JSON")
			return
		}

		var pub []byte
		if req.PublicKey != "" {
			decoded, err := base64.StdEncoding.DecodeString(req.PublicKey)
			if err != nil || len(decoded) != ed25519.PublicKeySize {
				writeError(w, http.StatusBadRequest, "открытый ключ машины неверной формы")
				return
			}
			pub = decoded
		}

		result, err := auth.Enroll(r.Context(), agents.EnrollRequest{
			EnrollmentToken: req.EnrollmentToken,
			AgentID:         req.AgentID,
			Hostname:        req.Hostname,
			AgentVersion:    req.AgentVersion,
			PublicKey:       pub,
		})

		switch {
		case errors.Is(err, agents.ErrDenied):
			log.Warn("отклонена регистрация", "ip", clientIP(r), "agent_id", req.AgentID, "err", err)
			// Причина не раскрывается: разница между «токена нет» и «срок
			// истёк» — подсказка тому, кто подбирает токен.
			writeError(w, http.StatusUnauthorized, "токен регистрации не принят")
			return
		case errors.Is(err, agents.ErrBadRequest):
			writeError(w, http.StatusBadRequest, err.Error())
			return
		case err != nil:
			log.Error("регистрация не удалась", "err", err, "agent_id", req.AgentID)
			writeError(w, http.StatusInternalServerError, "не удалось зарегистрировать агента")
			return
		}

		// Повторная регистрация той же отметки — не ошибка, но событие
		// заметное: так выглядит и переустановленная машина, и попытка
		// увести чужую отметку. В журнале это должно быть видно.
		if result.Rotated {
			log.Warn("ключ перевыпущен для уже известной отметки",
				"agent_id", result.Agent.AgentID, "ip", clientIP(r))
		} else {
			log.Info("агент зарегистрирован",
				"agent_id", result.Agent.AgentID, "hostname", result.Agent.Hostname)
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"agent_key":  result.Key, // виден первый и последний раз
			"agent_uuid": result.Agent.ID,
			"rotated":    result.Rotated,
		})
	}
}
