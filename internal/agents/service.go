package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/broton07/inventory_service/internal/storage"
	"github.com/broton07/inventory_service/pkg/contract"
)

// Ошибки, которые вызывающий различает по смыслу, а не по тексту.
var (
	// ErrDenied — ключ не подошёл. Одна ошибка на все причины намеренно:
	// разница между «ключа нет» и «ключ отозван» — это подсказка тому, кто
	// перебирает ключи.
	ErrDenied = errors.New("доступ запрещён")

	// ErrBadRequest — запрос неполон или противоречив.
	ErrBadRequest = errors.New("неверный запрос")
)

// Store — то, что этому пакету нужно от базы.
type Store interface {
	Enroll(ctx context.Context, in storage.EnrollParams) (storage.Agent, bool, error)
	FindAgentByKeyHash(ctx context.Context, keyHash []byte) (storage.Agent, error)
	FindAgentByMark(ctx context.Context, mark string) (storage.Agent, error)
	TouchAgent(ctx context.Context, id uuid.UUID, version, hostname string, now time.Time) error
}

// Service регистрирует агентов и проверяет их ключи.
type Service struct {
	store Store
	now   func() time.Time
}

// New создаёт службу.
func New(store Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}

// EnrollRequest — что присылает установщик.
type EnrollRequest struct {
	EnrollmentToken string
	AgentID         string
	Hostname        string
	AgentVersion    string

	// PublicKey — открытый ключ машины. Нужен не сейчас, а потом: ноутбук,
	// зарегистрировавшийся по сети, может однажды уехать надолго и прислать
	// отчёт файлом — проверять его подпись будет нечем, если ключ не взять
	// сразу. Пуст у агентов старых версий.
	PublicKey []byte
}

// EnrollResult — что получает агент. Ключ здесь виден первый и последний раз.
type EnrollResult struct {
	Agent   storage.Agent
	Key     string
	Rotated bool
}

// Enroll обменивает токен регистрации на персональный ключ машины.
func (s *Service) Enroll(ctx context.Context, req EnrollRequest) (EnrollResult, error) {
	token := strings.TrimSpace(req.EnrollmentToken)
	agentID := strings.TrimSpace(req.AgentID)

	if !LooksLikeEnrollmentToken(token) {
		// Форма проверяется до похода в базу: мусор и чужие заголовки не
		// должны превращаться в запрос на каждый вызов.
		return EnrollResult{}, fmt.Errorf("%w: токен регистрации не похож на токен", ErrDenied)
	}
	if agentID == "" {
		return EnrollResult{}, fmt.Errorf("%w: не указана отметка агента", ErrBadRequest)
	}
	if len(agentID) > 128 {
		return EnrollResult{}, fmt.Errorf("%w: отметка агента слишком длинная", ErrBadRequest)
	}

	key, err := NewAgentKey()
	if err != nil {
		return EnrollResult{}, err
	}

	agent, rotated, err := s.store.Enroll(ctx, storage.EnrollParams{
		TokenHash:    Hash(token),
		AgentID:      agentID,
		Hostname:     req.Hostname,
		AgentVersion: req.AgentVersion,
		KeyHash:      Hash(key),
		KeyPrefix:    DisplayPrefix(key),
		PublicKey:    req.PublicKey,
		Now:          s.now().UTC(),
	})

	if errors.Is(err, storage.ErrTokenUnusable) {
		// Причину не раскрываем: «срок истёк» против «такого токена нет» —
		// это подсказка тому, кто подбирает токен.
		return EnrollResult{}, fmt.Errorf("%w: %w", ErrDenied, err)
	}
	if err != nil {
		return EnrollResult{}, err
	}

	return EnrollResult{Agent: agent, Key: key, Rotated: rotated}, nil
}

// Authenticate проверяет ключ и возвращает агента.
func (s *Service) Authenticate(ctx context.Context, key string) (storage.Agent, error) {
	key = strings.TrimSpace(key)
	if !LooksLikeAgentKey(key) {
		return storage.Agent{}, ErrDenied
	}

	agent, err := s.store.FindAgentByKeyHash(ctx, Hash(key))
	if errors.Is(err, storage.ErrNotFound) {
		// Сюда попадают и несуществующий ключ, и отозванный: индекс, по
		// которому идёт поиск, отозванных не содержит.
		return storage.Agent{}, ErrDenied
	}
	if err != nil {
		return storage.Agent{}, err
	}

	return agent, nil
}

// Seen отмечает, что агент выходил на связь.
func (s *Service) Seen(ctx context.Context, agent storage.Agent, version, hostname string) error {
	return s.store.TouchAgent(ctx, agent.ID, version, hostname, s.now().UTC())
}

// EnrollOffline регистрирует машину по заявке, принесённой файлом.
//
// Путь тот же, что по сети, и право даёт тот же токен раскатки — меняется
// только транспорт. Разница в двух вещах: ключ доступа машина сгенерировала
// себе сама (служба получает лишь его хеш, как и по сети) и заявка подписана,
// чтобы открытый ключ нельзя было подменить по дороге.
func (s *Service) EnrollOffline(ctx context.Context, claim contract.SignedClaim) (storage.Agent, error) {
	token := strings.TrimSpace(claim.EnrollmentToken)
	mark := strings.TrimSpace(claim.AgentID)

	if !LooksLikeEnrollmentToken(token) {
		return storage.Agent{}, fmt.Errorf("%w: токен регистрации не похож на токен", ErrDenied)
	}
	if mark == "" {
		return storage.Agent{}, fmt.Errorf("%w: не указана отметка агента", ErrBadRequest)
	}

	pub, err := claim.Verify()
	if err != nil {
		// Заявка не сходится сама с собой: подпись подделать нельзя, а вот
		// подменить открытый ключ по дороге — можно, и ловится это здесь.
		return storage.Agent{}, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}

	keyHash, err := hex.DecodeString(strings.TrimSpace(claim.KeyHash))
	if err != nil || len(keyHash) != sha256.Size {
		return storage.Agent{}, fmt.Errorf("%w: хеш ключа доступа неверной формы", ErrBadRequest)
	}

	agent, _, err := s.store.Enroll(ctx, storage.EnrollParams{
		TokenHash:    Hash(token),
		AgentID:      mark,
		Hostname:     claim.Hostname,
		AgentVersion: claim.AgentVersion,
		KeyHash:      keyHash,
		KeyPrefix:    claim.KeyPrefix,
		PublicKey:    pub,
		Offline:      true,
		Now:          s.now().UTC(),
	})

	if errors.Is(err, storage.ErrTokenUnusable) {
		return storage.Agent{}, fmt.Errorf("%w: %w", ErrDenied, err)
	}
	if err != nil {
		return storage.Agent{}, err
	}
	return agent, nil
}

// ByMark находит агента по отметке и проверяет, что им ещё можно пользоваться.
//
// Нужен приёму автономных отчётов: в файле с флешки нет ни ключа, ни
// заголовка Authorization — только отметка, по которой берётся открытый ключ.
func (s *Service) ByMark(ctx context.Context, mark string) (storage.Agent, error) {
	agent, err := s.store.FindAgentByMark(ctx, strings.TrimSpace(mark))
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Agent{}, fmt.Errorf("%w: машина с такой отметкой не зарегистрирована", ErrDenied)
	}
	if err != nil {
		return storage.Agent{}, err
	}

	// Отзыв здесь называется своим именем, а не прячется за общим отказом: по
	// сети отказ обезличен, чтобы не подсказывать подбирающему ключ, а файл
	// приносит человек, и ему нужно понять, почему отчёт не приняли.
	if agent.Revoked() {
		return storage.Agent{}, fmt.Errorf("%w: ключ машины отозван (%s)", ErrDenied, agent.RevokeReason)
	}
	if len(agent.PublicKey) == 0 {
		return storage.Agent{}, fmt.Errorf(
			"%w: у машины нет открытого ключа — она регистрировалась агентом старой версии",
			ErrBadRequest)
	}

	return agent, nil
}
