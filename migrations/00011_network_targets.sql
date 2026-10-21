-- +goose Up

-- Что опрашивать по SNMP и чем кончилась последняя попытка.
--
-- Список приходит из двух мест: из настроек (NETSCAN_TARGETS, файл) и от
-- внешней системы через административный вход — учёт знает, какие коммутаторы
-- у него заведены и с какими адресами. source — кто положил строку: каждый
-- источник заменяет только свои строки и не трогает чужие.
--
-- Статус попытки живёт здесь, а не в network_devices: устройство, ни разу не
-- ответившее, туда не попадает (MAC шасси неизвестен), а человеку именно про
-- него и нужно знать, почему «не опрашивается».
CREATE TABLE network_targets (
    ip              text        PRIMARY KEY,
    name            text        NOT NULL DEFAULT '',
    source          text        NOT NULL,
    added_at        timestamptz NOT NULL DEFAULT now(),
    last_attempt_at timestamptz,
    last_ok_at      timestamptz,
    last_error      text        NOT NULL DEFAULT '',
    device_id       text        NOT NULL DEFAULT ''
);

CREATE INDEX network_targets_source_idx ON network_targets (source);

-- +goose Down
DROP TABLE IF EXISTS network_targets;
