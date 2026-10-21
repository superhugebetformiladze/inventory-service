-- +goose Up

-- Управляемые сетевые устройства, опрошенные по SNMP.
--
-- Последний снимок целиком и его отпечаток: по отпечатку решается, было ли
-- изменение (инвариант I9), — сравнивать JSONB по полям незачем, сравнение
-- понадобится потребителю, а не службе. Истории снимков нет намеренно: таблица
-- MAC-адресов меняется при каждом включении компьютера, и копить её снимки
-- — это гигабайты повторов. История «что куда было включено» — у потребителя.
CREATE TABLE network_devices (
    device_id      text        PRIMARY KEY,
    management_ip  text        NOT NULL,
    sys_name       text        NOT NULL DEFAULT '',
    first_seen_at  timestamptz NOT NULL,
    last_polled_at timestamptz NOT NULL,
    -- Когда снимок в последний раз отличался от предыдущего.
    changed_at     timestamptz NOT NULL,
    collected_at   timestamptz NOT NULL,
    payload        jsonb       NOT NULL,
    hash           text        NOT NULL
);

CREATE INDEX network_devices_ip_idx ON network_devices (management_ip);

-- +goose Down
DROP TABLE IF EXISTS network_devices;
