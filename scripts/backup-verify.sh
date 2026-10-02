#!/bin/sh
# Резервная копия базы с проверкой восстановлением.
#
# Копия, которую ни разу не восстанавливали, — это не копия, а файл. Проверить
# её можно ровно одним способом: восстановить и посмотреть, что данные на
# месте. Поэтому здесь два шага, а не один.
#
#   scripts/backup-verify.sh              снять и проверить
#   scripts/backup-verify.sh /куда/класть каталог для копии
#
# Запускается снаружи контейнеров, через compose: так же, как это будет делать
# задание по расписанию на сервере.
#
# Имена переменных латиницей намеренно: /bin/sh на сервере — не bash, и
# кириллическое имя переменной он не принимает вовсе.

set -e

DIR="${1:-./backups}"
CHECK_DB="inventory_restore_check"
# Какой стек копировать: по умолчанию разработческий, на сервере —
#   COMPOSE="docker compose --env-file .env.prod -f deploy/docker-compose.prod.yml"
COMPOSE="${COMPOSE:-docker compose --env-file .env -f deploy/docker-compose.yml}"

: "${POSTGRES_USER:?нужны переменные окружения из .env}"
: "${POSTGRES_DB:?нужны переменные окружения из .env}"

mkdir -p "$DIR"
FILE="$DIR/inventory-$(date +%Y%m%d-%H%M%S).dump"

echo "=== снимаю копию ==="
# Формат custom, а не plain SQL: он сжат, восстанавливается параллельно и
# позволяет достать отдельную таблицу, не разбирая гигабайтный текст.
$COMPOSE exec -T db pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc > "$FILE"
echo "копия: $FILE ($(wc -c < "$FILE") байт)"

echo
echo "=== восстанавливаю в проверочную базу ==="
$COMPOSE exec -T db psql -U "$POSTGRES_USER" -d postgres \
    -c "DROP DATABASE IF EXISTS $CHECK_DB" >/dev/null
$COMPOSE exec -T db psql -U "$POSTGRES_USER" -d postgres \
    -c "CREATE DATABASE $CHECK_DB" >/dev/null
$COMPOSE exec -T db pg_restore -U "$POSTGRES_USER" -d "$CHECK_DB" --no-owner < "$FILE"

echo
echo "=== сверяю содержимое ==="
# Сверяются не только числа строк, но и то, что снимок читается как JSON:
# повреждение jsonb внутри дампа иначе обнаружится в день восстановления.
$COMPOSE exec -T db psql -U "$POSTGRES_USER" -d "$CHECK_DB" -c "
SELECT 'машин'      AS таблица, count(*) AS строк FROM hosts
UNION ALL SELECT 'снимков',   count(*) FROM snapshots
UNION ALL SELECT 'изменений', count(*) FROM changes
UNION ALL SELECT 'агентов',   count(*) FROM agents
UNION ALL SELECT 'снимков с читаемым JSON',
                 count(*) FROM snapshots WHERE payload ? 'schema_version';"

$COMPOSE exec -T db psql -U "$POSTGRES_USER" -d postgres \
    -c "DROP DATABASE $CHECK_DB" >/dev/null

echo
echo "копия восстанавливается, данные на месте: $FILE"
