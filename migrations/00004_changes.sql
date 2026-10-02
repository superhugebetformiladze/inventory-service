-- +goose Up

-- Отпечаток последнего снимка машины — по тем полям, которые вообще
-- сравниваются.
--
-- Это быстрый путь для самого частого случая: девятнадцать отчётов из
-- двадцати ничем не отличаются от предыдущего, и сравнение двух хешей стоит
-- дешевле, чем разбор и сличение двух снимков.
ALTER TABLE hosts ADD COLUMN fingerprint bytea;

-- С какого момента машина молчит. NULL — на связи.
--
-- Отдельная колонка, а не вычисление «last_report_at < порога» на лету:
-- событие о молчании должно уйти РОВНО ОДИН раз, а не на каждом проходе
-- задачи, пока машина выключена в отпуске.
ALTER TABLE hosts ADD COLUMN silent_since timestamptz;

CREATE INDEX hosts_silence_idx ON hosts (last_report_at) WHERE silent_since IS NULL;

-- История изменений железа. То, ради чего всё и затевалось: ответ на вопрос
-- «кто вынул диск и когда».
--
-- Живёт дольше снимков: снимки чистятся по сроку, а эта таблица — сама
-- история, и стирать её нельзя.
CREATE TABLE changes (
    id          bigserial   PRIMARY KEY,
    host_id     uuid        NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
    snapshot_id uuid        NOT NULL REFERENCES snapshots (id) ON DELETE CASCADE,
    detected_at timestamptz NOT NULL DEFAULT now(),

    field     text NOT NULL,
    old_value text NOT NULL,
    new_value text NOT NULL
);

CREATE INDEX changes_host_idx ON changes (host_id, detected_at DESC);

-- +goose Down
DROP TABLE changes;
DROP INDEX hosts_silence_idx;
ALTER TABLE hosts DROP COLUMN silent_since;
ALTER TABLE hosts DROP COLUMN fingerprint;
