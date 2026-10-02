-- +goose Up

-- Одна запись в очереди разбора на одну машину, а не одна на каждый отчёт.
--
-- Найдено живым прогоном на седьмом этапе. Машина, которую не удаётся
-- опознать, шлёт отчёты дальше — каждые четыре часа, и каждый ложился в
-- очередь отдельной строкой. Одна такая машина даёт шесть строк в сутки; на
-- парке, где заводского номера нет у трети машин, очередь разбора за неделю
-- перестаёт быть списком дел и становится журналом.
--
-- Теперь повторный отчёт от той же машины обновляет уже лежащую запись:
-- человек видит одну строку, свежий снимок в ней и счётчик — сколько раз
-- машина приходила, пока её не разобрали.
ALTER TABLE unresolved_reports ADD COLUMN attempts integer NOT NULL DEFAULT 1;
ALTER TABLE unresolved_reports ADD COLUMN first_seen_at timestamptz;

UPDATE unresolved_reports SET first_seen_at = received_at WHERE first_seen_at IS NULL;

-- Схлопывание того, что уже накопилось: оставляем самый свежий отчёт каждой
-- машины, остальные помечаем разобранными, чтобы не потерять их совсем.
WITH ранжированные AS (
    SELECT id, agent_uuid,
           row_number() OVER (PARTITION BY agent_uuid ORDER BY received_at DESC) AS место,
           count(*)     OVER (PARTITION BY agent_uuid)                           AS всего,
           min(received_at) OVER (PARTITION BY agent_uuid)                       AS впервые
      FROM unresolved_reports
     WHERE resolved_at IS NULL AND agent_uuid IS NOT NULL
)
UPDATE unresolved_reports u
   SET attempts      = р.всего,
       first_seen_at = р.впервые
  FROM ранжированные р
 WHERE u.id = р.id AND р.место = 1;

DELETE FROM unresolved_reports u
 USING (
    SELECT id, row_number() OVER (PARTITION BY agent_uuid ORDER BY received_at DESC) AS место
      FROM unresolved_reports
     WHERE resolved_at IS NULL AND agent_uuid IS NOT NULL
 ) р
 WHERE u.id = р.id AND р.место > 1;

-- Ограничение, а не соглашение: без него договорённость «одна строка на
-- машину» держалась бы на том, что все запросы её помнят.
CREATE UNIQUE INDEX unresolved_one_per_agent_idx ON unresolved_reports (agent_uuid)
    WHERE resolved_at IS NULL AND agent_uuid IS NOT NULL;

-- +goose Down
DROP INDEX unresolved_one_per_agent_idx;
ALTER TABLE unresolved_reports DROP COLUMN first_seen_at;
ALTER TABLE unresolved_reports DROP COLUMN attempts;
