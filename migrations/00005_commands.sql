-- +goose Up

-- Очередь заданий агентам.
--
-- Задание адресовано АГЕНТУ, а не машине: спрашивает сервер именно агент, по
-- своему ключу. Ссылка на машину рядом — чтобы человек мог сказать «собери с
-- ПК-214», не зная про отметки агентов.
CREATE TABLE commands (
    id      uuid PRIMARY KEY,
    agent_uuid uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    host_id    uuid          REFERENCES hosts (id)  ON DELETE SET NULL,

    kind   text  NOT NULL,
    params jsonb NOT NULL DEFAULT '{}',

    created_at timestamptz NOT NULL DEFAULT now(),
    created_by text        NOT NULL DEFAULT '',

    -- Срок годности задания.
    --
    -- Без него «собрать сейчас», поставленное в понедельник ноутбуку,
    -- уехавшему в отпуск, сработает через три недели — когда никому уже не
    -- нужно и никто не помнит, зачем просили.
    expires_at timestamptz NOT NULL,

    -- Когда задание выдано агенту. Выдача не окончательна: агент мог упасть
    -- сразу после получения, и по истечении срока аренды задание снова
    -- становится доступным.
    taken_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,

    done_at timestamptz,
    ok      boolean,
    error   text NOT NULL DEFAULT ''
);

-- Опрос заданий агент делает раз в минуту, и почти всегда впустую. Индекс
-- частичный, чтобы этот самый частый запрос не трогал выполненные задания.
CREATE INDEX commands_pending_idx ON commands (agent_uuid, created_at)
    WHERE done_at IS NULL;

-- Текущий агент машины: через него человек ставит задание, не зная отметок.
--
-- Колонка заведена ещё на третьем этапе, но не заполнялась: до опознания было
-- неоткуда взять надёжную связь. Теперь она проставляется при каждом
-- принятом отчёте.
UPDATE hosts h
   SET agent_uuid = a.id
  FROM agents a
 WHERE a.agent_id = h.agent_id AND h.agent_uuid IS NULL;

-- +goose Down
DROP TABLE commands;
