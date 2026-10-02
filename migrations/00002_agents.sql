-- +goose Up

-- Токен регистрации: то, что кладут в установщик агента.
--
-- Он не даёт права слать отчёты — только право обменяться на персональный
-- ключ. Разница существенная: токен один на всю раскатку и рано или поздно
-- утечёт (он лежит в скрипте групповой политики, который видит весь домен),
-- поэтому он ограничен сроком и числом использований, а отзывать по одной
-- машине можно уже выданные ключи, а не его.
CREATE TABLE enrollment_tokens (
    id         uuid        PRIMARY KEY,
    label      text        NOT NULL DEFAULT '',

    -- Только хеш. Сам токен печатается один раз при создании и больше нигде
    -- не хранится: база, из которой можно достать действующий токен, — это
    -- база, из которой можно зарегистрировать чужую машину.
    token_hash bytea       NOT NULL UNIQUE,

    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,

    -- NULL — без ограничения по числу использований (для массовой раскатки).
    uses_left  integer,
    used_count integer     NOT NULL DEFAULT 0,
    revoked_at timestamptz
);

-- Агент — зарегистрированная программа на конкретной машине.
CREATE TABLE agents (
    id       uuid PRIMARY KEY,

    -- Отметка агента: идентификатор, который он записал у себя при первом
    -- запуске. Именно она связывает ключ с машиной.
    agent_id text NOT NULL UNIQUE,

    -- Хеш персонального ключа. Поиск идёт по нему напрямую, поэтому индекс
    -- уникальный: одинаковых ключей быть не может.
    key_hash   bytea NOT NULL UNIQUE,

    -- Первые символы ключа — чтобы человек мог узнать его в журнале и в
    -- списке агентов, не имея самого ключа.
    key_prefix text  NOT NULL,

    hostname      text NOT NULL DEFAULT '',
    agent_version text NOT NULL DEFAULT '',

    enrolled_at        timestamptz NOT NULL DEFAULT now(),
    key_rotated_at     timestamptz,
    last_seen_at       timestamptz,
    enrollment_token_id uuid REFERENCES enrollment_tokens (id),

    -- Отзыв по одной машине. Не удаление: удалённый агент через час
    -- зарегистрируется заново и будет выглядеть новым, а отозванный виден в
    -- списке как отозванный.
    revoked_at    timestamptz,
    revoke_reason text NOT NULL DEFAULT ''
);

-- Проверка ключа при каждом отчёте идёт по этому индексу; отозванные из
-- выборки исключены, поэтому индекс частичный.
CREATE INDEX agents_active_idx ON agents (key_hash) WHERE revoked_at IS NULL;

-- Связь машины с агентом. Отдельной колонкой, а не заменой hosts.agent_id:
-- на четвёртом этапе опознание перестанет опираться на отметку агента, и
-- текстовое поле уйдёт, а эта ссылка останется.
ALTER TABLE hosts ADD COLUMN agent_uuid uuid REFERENCES agents (id);

-- +goose Down
ALTER TABLE hosts DROP COLUMN agent_uuid;
DROP TABLE agents;
DROP TABLE enrollment_tokens;
