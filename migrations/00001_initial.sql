-- +goose Up

-- Машина. На этом этапе опознание сведено к отметке агента; цепочка признаков
-- (заводской номер, адрес карты, очередь разбора) появится на этапе 4 и
-- добавит таблицу host_identifiers. Здесь важно не опознание, а то, что путь
-- отчёта проходит насквозь.
CREATE TABLE hosts (
    id             uuid        PRIMARY KEY,
    agent_id       text        NOT NULL UNIQUE,
    hostname       text        NOT NULL DEFAULT '',
    first_seen_at  timestamptz NOT NULL,
    last_report_at timestamptz NOT NULL
);

-- Снимок целиком. payload — тот же JSON, что прислал агент, без потерь:
-- разбирать его в колонки значило бы терять всё, о чём мы ещё не догадались
-- спросить, а состав машин меняется чаще, чем схема базы.
CREATE TABLE snapshots (
    id            uuid        PRIMARY KEY,
    host_id       uuid        NOT NULL REFERENCES hosts (id),
    collected_at  timestamptz NOT NULL,
    received_at   timestamptz NOT NULL,
    origin        text        NOT NULL,
    agent_version text        NOT NULL DEFAULT '',
    payload       jsonb       NOT NULL,
    payload_hash  bytea       NOT NULL,

    -- Идемпотентность приёма (инвариант I3). Агент повторяет отправку при
    -- обрыве связи, и без этого ограничения каждый обрыв оставлял бы в базе
    -- лишний снимок. Ключ — время сбора: повтор несёт то же самое время,
    -- новый сбор — другое.
    CONSTRAINT snapshots_host_collected_key UNIQUE (host_id, collected_at)
);

CREATE INDEX snapshots_host_recent_idx ON snapshots (host_id, collected_at DESC);

-- Транзакционный outbox (инвариант I2).
--
-- Смысл таблицы в том, что «записать снимок» и «опубликовать событие» — одна
-- транзакция. Публикация прямо из обработчика запроса означала бы: упали
-- между записью и отправкой — событие потеряно навсегда, и никто об этом не
-- узнает, потому что в базе всё на месте.
CREATE TABLE outbox (
    id           bigserial   PRIMARY KEY,
    topic        text        NOT NULL,
    key          text        NOT NULL,
    payload      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text
);

-- Частичный индекс: релей читает только неопубликованные, а их в норме
-- единицы. Полный индекс по таблице, которая растёт на каждый отчёт, стоил бы
-- места и времени вставки ни за что.
CREATE INDEX outbox_pending_idx ON outbox (id) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE outbox;
DROP TABLE snapshots;
DROP TABLE hosts;
