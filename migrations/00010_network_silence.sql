-- +goose Up

-- Молчание сетевых устройств.
--
-- Состояние коммутатора публикуется только при изменении (I9), и по ленте
-- нельзя отличить «опрашивается, ничего не меняется» от «давно не отвечает».
-- Поэтому, как и у машин, молчание — само по себе изменение состояния:
-- failing_since — с какого опроса подряд устройство не отвечает,
-- silent_since — когда об этом сообщено в ленту (и не раньше порога).
ALTER TABLE network_devices ADD COLUMN failing_since timestamptz;
ALTER TABLE network_devices ADD COLUMN silent_since timestamptz;
ALTER TABLE network_devices ADD COLUMN last_error text NOT NULL DEFAULT '';

CREATE INDEX network_devices_failing_idx ON network_devices (failing_since)
    WHERE failing_since IS NOT NULL AND silent_since IS NULL;

-- +goose Down
DROP INDEX IF EXISTS network_devices_failing_idx;
ALTER TABLE network_devices DROP COLUMN last_error;
ALTER TABLE network_devices DROP COLUMN silent_since;
ALTER TABLE network_devices DROP COLUMN failing_since;
