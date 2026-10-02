-- +goose Up

-- Признаки, по которым машина опознаётся.
--
-- Первичный ключ на (kind, value) — это и есть главное ограничение всей
-- затеи: один признак принадлежит не более чем одной машине. Не соглашение в
-- коде, которое однажды нарушат, а правило базы, которое нарушить нельзя.
CREATE TABLE host_identifiers (
    kind          text        NOT NULL,   -- agent | serial | uuid | mac
    value         text        NOT NULL,
    host_id       uuid        NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (kind, value)
);

CREATE INDEX host_identifiers_host_idx ON host_identifiers (host_id);

-- Отчёты, которые не удалось отнести к машине.
--
-- Отчёт сохраняется целиком: человеку, который будет разбираться, нужны не
-- «какие-то признаки не совпали», а сам снимок — по нему видно, что за машина
-- и откуда она взялась.
CREATE TABLE unresolved_reports (
    id          uuid        PRIMARY KEY,
    agent_uuid  uuid        REFERENCES agents (id),
    received_at timestamptz NOT NULL DEFAULT now(),

    reason     text  NOT NULL,
    candidates jsonb NOT NULL DEFAULT '[]',
    payload    jsonb NOT NULL,

    resolved_at      timestamptz,
    resolved_host_id uuid REFERENCES hosts (id),
    resolved_by      text NOT NULL DEFAULT ''
);

CREATE INDEX unresolved_pending_idx ON unresolved_reports (received_at)
    WHERE resolved_at IS NULL;

-- Перенос того, что уже опознано отметкой агента на прошлых этапах.
INSERT INTO host_identifiers (kind, value, host_id)
SELECT 'agent', agent_id, id FROM hosts
ON CONFLICT DO NOTHING;

-- Отметка агента перестаёт быть ключом: у одной машины их за жизнь бывает
-- несколько (переустановка системы), и уникальность здесь начала бы мешать.
-- Колонка остаётся как «последняя известная отметка» — её видно в списках.
ALTER TABLE hosts DROP CONSTRAINT hosts_agent_id_key;

-- +goose Down
ALTER TABLE hosts ADD CONSTRAINT hosts_agent_id_key UNIQUE (agent_id);
DROP TABLE unresolved_reports;
DROP TABLE host_identifiers;
