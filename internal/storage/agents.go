package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Agent — зарегистрированная программа на конкретной машине.
type Agent struct {
	ID           uuid.UUID
	AgentID      string
	KeyPrefix    string
	Hostname     string
	AgentVersion string
	EnrolledAt   time.Time
	LastSeenAt   *time.Time
	RevokedAt    *time.Time
	RevokeReason string

	// PublicKey — открытый ключ машины. Им проверяется подпись отчёта,
	// принесённого файлом. Пуст у агентов, зарегистрированных до того, как
	// автономный режим появился.
	PublicKey []byte

	// EnrolledOffline — машина зарегистрирована заявкой с флешки, а не по
	// сети. У таких «на связи» не двигается никогда, и без этой пометки они
	// неотличимы от замолчавших.
	EnrolledOffline bool
}

// Revoked — отозван ли агент.
func (a Agent) Revoked() bool { return a.RevokedAt != nil }

// EnrollmentToken — токен регистрации без самого секрета.
type EnrollmentToken struct {
	ID        uuid.UUID
	Label     string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsesLeft  *int
	UsedCount int
	RevokedAt *time.Time
}

// ErrTokenUnusable — токен есть, но воспользоваться им нельзя.
var ErrTokenUnusable = errors.New("токен регистрации недействителен")

// CreateEnrollmentToken сохраняет хеш нового токена.
func (p *Pool) CreateEnrollmentToken(
	ctx context.Context, hash []byte, label string, expiresAt time.Time, usesLeft *int,
) (EnrollmentToken, error) {
	t := EnrollmentToken{ID: uuid.New(), Label: label, ExpiresAt: expiresAt, UsesLeft: usesLeft}

	err := p.QueryRow(ctx, `
		INSERT INTO enrollment_tokens (id, label, token_hash, expires_at, uses_left)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`,
		t.ID, label, hash, expiresAt, usesLeft).Scan(&t.CreatedAt)
	if err != nil {
		return EnrollmentToken{}, fmt.Errorf("создание токена регистрации: %w", err)
	}
	return t, nil
}

// EnrollParams — всё, что нужно для регистрации одной машины.
//
// Структурой, а не восемью позиционными аргументами: половина из них — байтовые
// срезы и строки, и перепутанные местами keyHash с publicKey компилятор бы не
// заметил.
type EnrollParams struct {
	TokenHash []byte

	AgentID      string
	Hostname     string
	AgentVersion string

	// KeyHash и KeyPrefix — ключ доступа машины. Сам ключ службе неизвестен:
	// по сети он генерируется здесь и тут же отдаётся агенту, из заявки с
	// флешки приходит уже готовый хеш.
	KeyHash   []byte
	KeyPrefix string

	// PublicKey — открытый ключ машины для проверки подписи автономных
	// отчётов. Пуст, если агент старой версии его не прислал.
	PublicKey []byte

	// Offline — регистрация заявкой с флешки, а не по сети.
	Offline bool

	Now time.Time
}

// Enroll обменивает токен регистрации на запись агента.
//
// Всё одной транзакцией: проверка токена, списание использования и создание
// агента. Иначе два установщика, стартовавшие одновременно с токеном на одно
// применение, оба увидели бы «остался один» и оба зарегистрировались.
//
// FOR UPDATE на токене — та самая блокировка, которая это предотвращает.
func (p *Pool) Enroll(ctx context.Context, in EnrollParams) (Agent, bool, error) {
	tokenHash := in.TokenHash
	now := in.Now
	var (
		agent   Agent
		rotated bool
	)

	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		var (
			tokenID   uuid.UUID
			expiresAt time.Time
			usesLeft  *int
			revokedAt *time.Time
		)

		err := tx.QueryRow(ctx, `
			SELECT id, expires_at, uses_left, revoked_at
			  FROM enrollment_tokens
			 WHERE token_hash = $1
			 FOR UPDATE`, tokenHash).Scan(&tokenID, &expiresAt, &usesLeft, &revokedAt)

		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenUnusable
		}
		if err != nil {
			return fmt.Errorf("поиск токена: %w", err)
		}

		switch {
		case revokedAt != nil:
			return fmt.Errorf("%w: отозван", ErrTokenUnusable)
		case now.After(expiresAt):
			return fmt.Errorf("%w: срок истёк", ErrTokenUnusable)
		case usesLeft != nil && *usesLeft <= 0:
			return fmt.Errorf("%w: использования исчерпаны", ErrTokenUnusable)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE enrollment_tokens
			   SET used_count = used_count + 1,
			       uses_left  = CASE WHEN uses_left IS NULL THEN NULL ELSE uses_left - 1 END
			 WHERE id = $1`, tokenID); err != nil {
			return fmt.Errorf("списание использования токена: %w", err)
		}

		// Повторная регистрация той же отметки — не ошибка. Так выглядит
		// установщик, который получил ключ, но не успел его сохранить, и
		// машина, переустановленная с сохранением отметки. Ключ при этом
		// меняется: старый после переустановки всё равно потерян.
		agent.ID = uuid.New()
		err = tx.QueryRow(ctx, `
			INSERT INTO agents (id, agent_id, key_hash, key_prefix, hostname, agent_version,
			                    enrollment_token_id, public_key, enrolled_offline)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $9, $10)
			ON CONFLICT (agent_id) DO UPDATE
			   SET key_hash       = EXCLUDED.key_hash,
			       key_prefix     = EXCLUDED.key_prefix,
			       hostname       = EXCLUDED.hostname,
			       agent_version  = EXCLUDED.agent_version,
			       key_rotated_at = $8,
			       revoked_at     = NULL,
			       revoke_reason  = '',
			       -- Открытый ключ не затирается пустым: агент старой версии,
			       -- перерегистрировавшись, иначе лишил бы машину возможности
			       -- присылать подписанные отчёты, и починить это можно было
			       -- бы только руками.
			       public_key       = COALESCE(EXCLUDED.public_key, agents.public_key),
			       enrolled_offline = EXCLUDED.enrolled_offline
			RETURNING id, agent_id, key_prefix, hostname, agent_version, enrolled_at,
			          last_seen_at, revoked_at, revoke_reason, public_key, enrolled_offline,
			          key_rotated_at IS NOT NULL`,
			agent.ID, in.AgentID, in.KeyHash, in.KeyPrefix, in.Hostname, in.AgentVersion,
			tokenID, now, nullBytes(in.PublicKey), in.Offline,
		).Scan(&agent.ID, &agent.AgentID, &agent.KeyPrefix, &agent.Hostname, &agent.AgentVersion,
			&agent.EnrolledAt, &agent.LastSeenAt, &agent.RevokedAt, &agent.RevokeReason,
			&agent.PublicKey, &agent.EnrolledOffline, &rotated)
		if err != nil {
			return fmt.Errorf("регистрация агента: %w", err)
		}

		return nil
	})

	if err != nil {
		return Agent{}, false, err
	}
	return agent, rotated, nil
}

// nullBytes отдаёт NULL вместо пустого среза.
//
// Нужно ровно для COALESCE выше: пустой срез Postgres примет как значение и
// затрёт хранившийся открытый ключ.
func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// agentColumns — общий список полей агента: три запроса читают одно и то же, и
// разъезжаться им нельзя.
const agentColumns = `id, agent_id, key_prefix, hostname, agent_version, enrolled_at,
	       last_seen_at, revoked_at, revoke_reason, public_key, enrolled_offline`

// scanAgent разбирает строку в том же порядке, в каком её объявляет
// agentColumns.
func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.ID, &a.AgentID, &a.KeyPrefix, &a.Hostname, &a.AgentVersion,
		&a.EnrolledAt, &a.LastSeenAt, &a.RevokedAt, &a.RevokeReason,
		&a.PublicKey, &a.EnrolledOffline)
	return a, err
}

// FindAgentByKeyHash ищет действующего агента по хешу ключа.
//
// Поиск по хешу, а не перебор с проверкой: ключ — 256 случайных бит, его
// хеш уникален, и один запрос по индексу даёт ответ. Отозванные исключены
// самим индексом.
func (p *Pool) FindAgentByKeyHash(ctx context.Context, keyHash []byte) (Agent, error) {
	a, err := scanAgent(p.QueryRow(ctx, `
		SELECT `+agentColumns+`
		  FROM agents
		 WHERE key_hash = $1 AND revoked_at IS NULL`, keyHash))

	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, fmt.Errorf("поиск агента по ключу: %w", err)
	}
	return a, nil
}

// FindAgentByMark ищет агента по его отметке.
//
// Нужен приёму автономных отчётов: в файле с флешки нет ни ключа, ни
// заголовка Authorization — есть только отметка, по которой берётся открытый
// ключ для проверки подписи.
//
// Отозванные НЕ исключаются: отзыв должен быть виден как отказ с объяснением,
// а не как «такой машины нет». Проверяет вызывающий.
func (p *Pool) FindAgentByMark(ctx context.Context, mark string) (Agent, error) {
	a, err := scanAgent(p.QueryRow(ctx, `
		SELECT `+agentColumns+`
		  FROM agents
		 WHERE agent_id = $1`, mark))

	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, fmt.Errorf("поиск агента по отметке: %w", err)
	}
	return a, nil
}

// TouchAgent двигает отметку «на связи» и запоминает версию агента.
//
// Версия обновляется на каждом отчёте не из педантизма: после первого же
// обновления в парке живут две-три версии, и на вопрос «почему у этих машин
// нет поля X» отвечает только это поле.
func (p *Pool) TouchAgent(ctx context.Context, id uuid.UUID, version, hostname string, now time.Time) error {
	_, err := p.Exec(ctx, `
		UPDATE agents
		   SET last_seen_at  = GREATEST(COALESCE(last_seen_at, $2), $2),
		       agent_version = COALESCE(NULLIF($3, ''), agent_version),
		       hostname      = COALESCE(NULLIF($4, ''), hostname)
		 WHERE id = $1`, id, now, version, hostname)
	if err != nil {
		return fmt.Errorf("отметка агента на связи: %w", err)
	}
	return nil
}

// RevokeAgent отзывает ключ одной машины.
func (p *Pool) RevokeAgent(ctx context.Context, agentID, reason string, now time.Time) error {
	tag, err := p.Exec(ctx, `
		UPDATE agents
		   SET revoked_at = $2, revoke_reason = $3
		 WHERE agent_id = $1 AND revoked_at IS NULL`, agentID, now, reason)
	if err != nil {
		return fmt.Errorf("отзыв агента: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAgents — список агентов, свежие сверху.
func (p *Pool) ListAgents(ctx context.Context, limit int) ([]Agent, error) {
	rows, err := p.Query(ctx, `
		SELECT `+agentColumns+`
		  FROM agents
		 ORDER BY COALESCE(last_seen_at, enrolled_at) DESC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("список агентов: %w", err)
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
