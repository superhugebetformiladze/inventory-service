# Развёртывание на локальном сервере

Инструкция для текущей версии. Всё крутится в Docker: PostgreSQL, API на FastAPI и nginx
со статикой. Наружу смотрит только nginx.

Проверено на Docker 27 / Compose 2.29. Порядок написан для Linux-сервера; отличия для
Windows Server — в конце.

---

## 1. Что нужно на сервере

- Docker Engine 24+ и плагин Docker Compose v2 (`docker compose version`);
- 2 ГБ ОЗУ и 2 ядра хватает с запасом; под данные — от 10 ГБ
  (одна выгрузка ≈ 5 МБ на диске плюс примерно столько же в БД);
- свободные порты 9080 и 9443 (меняются в `.env`, см. раздел 3);
- сеть: доступ пользователей к серверу по HTTP(S); наружу в интернет сервису ходить не нужно.

Установка Docker на Ubuntu/Debian, если его ещё нет:

```bash
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER   # перелогиниться после этого
```

## 2. Забрать проект на сервер

```bash
sudo mkdir -p /opt/xml_parser && sudo chown $USER /opt/xml_parser
cd /opt/xml_parser
# вариант А: из git
git clone <адрес-репозитория> .
# вариант Б: скопировать папку проекта целиком (scp/rsync/флешка)
```

Каталог `sample_data/` на сервере не нужен, его можно не копировать.

## 3. Настроить .env

```bash
cp .env.example .env
```

Обязательно задайте три значения:

```bash
# случайный ключ подписи токенов
docker run --rm python:3.12-slim python -c "import secrets; print(secrets.token_urlsafe(48))"
# пароли
openssl rand -base64 18   # для POSTGRES_PASSWORD
openssl rand -base64 18   # для ADMIN_PASSWORD
```

Впишите их в `.env`:

```ini
POSTGRES_USER=xml
POSTGRES_PASSWORD=<сгенерированный пароль БД>
POSTGRES_DB=xml_parser

SECRET_KEY=<сгенерированный ключ>

ADMIN_USERNAME=admin
ADMIN_PASSWORD=<сгенерированный пароль админа>

MAX_UPLOAD_MB=30

# Порты, на которых сервис виден снаружи.
# Поменяйте, если эти заняты: значения подхватятся и nginx, и редиректом с http.
HTTP_PORT=9080
HTTPS_PORT=9443
```

Файл `.env` содержит пароли — закройте к нему доступ: `chmod 600 .env`.

Имя админа и пароль применяются **один раз**, при первом старте на пустой базе.
Потом пароль меняется только в интерфейсе.

## 4. Сертификат для HTTPS

Без HTTPS пароли и токены идут по сети открытым текстом — в локальной сети это тоже
читается любым, кто слушает трафик.

**Вариант А. Сертификат от вашего удостоверяющего центра** (если в организации есть свой CA
или закупленный сертификат) — положите файлы как:

```
deploy/certs/server.crt   # сертификат + промежуточные, в одном файле
deploy/certs/server.key   # приватный ключ
```

**Вариант Б. Самоподписанный** — годится для внутренней сети, но браузер будет ругаться,
пока сертификат не добавят в доверенные на машинах пользователей (или через групповые политики):

```bash
mkdir -p deploy/certs
docker run --rm -v "$PWD/deploy/certs:/certs" alpine/openssl req -x509 -nodes \
  -newkey rsa:2048 -days 825 \
  -keyout /certs/server.key -out /certs/server.crt \
  -subj "/CN=xml-parser.local" \
  -addext "subjectAltName=DNS:xml-parser.local,IP:192.168.1.50"
chmod 600 deploy/certs/server.key
```

В `subjectAltName` подставьте реальное имя и IP сервера — иначе браузер не примет сертификат
даже после добавления в доверенные.

## 5. Запуск

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

Первая сборка занимает несколько минут. Что даёт прод-оверрайд:

- порт API наружу **не публикуется** — только через nginx;
- Swagger выключен;
- nginx слушает `HTTP_PORT` (редирект на HTTPS) и `HTTPS_PORT`;
- контейнеры поднимаются сами после перезагрузки сервера (`restart: always`);
- логи контейнеров ограничены 3 файлами по 10 МБ.

Проверка:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml ps
curl -k https://localhost:9443/api/health   # {"status":"ok"}
curl -s http://localhost:9080/healthz       # ok
```

В `docker compose ps` у `backend` и `db` в колонке портов не должно быть `0.0.0.0` —
наружу они не смотрят.

### Про нестандартные порты и адрес для пользователей

Раздавайте коллегам **адрес с HTTPS-портом**: `https://<сервер>:9443/`.

Порт 9080 оставлен только как подсказка для тех, кто наберёт адрес без `https`: nginx
перебросит их на 9443. Но сработает это один раз. После первого успешного входа по HTTPS
браузер запоминает для этого хоста правило HSTS и дальше сам подставляет `https` к любому
адресу, **сохраняя порт**: `http://сервер:9080` превратится в `https://сервер:9080`, где
TLS никто не обслуживает, и будет ошибка подключения. Это не поломка конфигурации, а то,
как HSTS устроен: он привязан к имени хоста, а не к порту.

Если такая полумера мешает, уберите публикацию HTTP-порта: в `docker-compose.prod.yml`
удалите строку с `${HTTP_PORT:-9080}:80`. Тогда единственным рабочим адресом останется
`https://<сервер>:9443/`, а попытка зайти по HTTP даст понятный отказ соединения.

## 6. Первый вход

1. Откройте `https://<адрес-сервера>:9443/` в браузере.
2. Войдите под `ADMIN_USERNAME` / `ADMIN_PASSWORD` из `.env`.
3. **Сразу смените пароль**: «Админка» → «Пользователи» → «пароль».
4. Заведите учётки: роль «Просмотр» для тех, кто только смотрит отчёты,
   «Администратор» — для тех, кто грузит файлы.
5. «Админка» → «Файлы» → загрузите XML-выгрузку.
6. «Админка» → «Оборудование» → загрузите таблицу оборудования (Excel). После импорта
   проверьте привязки, помеченные как «по порядку»: код MDM общий для одинаковых моделей,
   и сопоставление инвентарного номера с конкретным станком — предположение.

## 7. Firewall

Откройте только те порты, что указаны в `.env` (по умолчанию 9080 и 9443):

```bash
# Ubuntu (ufw)
sudo ufw allow 9080/tcp && sudo ufw allow 9443/tcp && sudo ufw enable
# RHEL/CentOS (firewalld)
sudo firewall-cmd --permanent --add-port=9080/tcp --add-port=9443/tcp && sudo firewall-cmd --reload
```

Внимание: Docker по умолчанию пишет правила в iptables в обход ufw. В нашей конфигурации
наружу публикует порты только nginx, но проверьте, что снаружи не видно лишнего:

```bash
sudo ss -tlnp | grep docker
```

## 8. Резервное копирование

Данные лежат в двух томах Docker: `xml_parser_pgdata` (база) и `xml_parser_uploads`
(исходные XML). Скрипт ежедневного бэкапа:

```bash
sudo tee /usr/local/bin/xml_parser_backup.sh >/dev/null <<'EOF'
#!/bin/bash
set -e
DEST=/var/backups/xml_parser
DATE=$(date +%F)
mkdir -p "$DEST"
cd /opt/xml_parser
docker compose -f docker-compose.yml -f docker-compose.prod.yml exec -T db \
  pg_dump -U xml xml_parser | gzip > "$DEST/db-$DATE.sql.gz"
docker run --rm -v xml_parser_uploads:/data:ro -v "$DEST:/backup" alpine \
  tar czf "/backup/uploads-$DATE.tar.gz" -C /data .
cp /opt/xml_parser/.env "$DEST/env-$DATE.bak"
find "$DEST" -type f -mtime +30 -delete
EOF
sudo chmod +x /usr/local/bin/xml_parser_backup.sh
echo "30 2 * * * root /usr/local/bin/xml_parser_backup.sh" | sudo tee /etc/cron.d/xml_parser_backup
```

Восстановление:

```bash
cd /opt/xml_parser
# база
gunzip -c /var/backups/xml_parser/db-2026-09-17.sql.gz | \
  docker compose -f docker-compose.yml -f docker-compose.prod.yml exec -T db psql -U xml -d xml_parser
# файлы
docker run --rm -v xml_parser_uploads:/data -v /var/backups/xml_parser:/backup alpine \
  tar xzf /backup/uploads-2026-09-17.tar.gz -C /data
```

Бэкап без `.env` бесполезен наполовину: с другим `SECRET_KEY` разъедутся сессии,
а с другим паролем БД дамп не подключить.

## 9. Обновление версии

```bash
cd /opt/xml_parser
/usr/local/bin/xml_parser_backup.sh          # сначала бэкап
git pull                                      # или rsync, см. ниже
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
docker image prune -f                         # убрать старые слои
```

**Важно про доставку файлов.** Между версиями файлы не только меняются, но и удаляются
или переименовываются. Простое копирование поверх (`scp -r`, перетаскивание папки)
добавит новые файлы, но оставит старые — и сборка упадёт на конфликте: например,
`src/api.js` из прошлой версии перекроет новую папку `src/api/`.

Правильно — `git pull` либо rsync с ключом `--delete`:

```bash
rsync -a --delete --exclude node_modules --exclude .env --exclude deploy/certs   ./ user@server:/opt/xml_parser/
```

Если всё же скопировали поверх и сборка ругается на «Could not find a declaration file»,
удалите остатки прошлой версии — в текущей во фронтенде нет ни одного файла .js или .jsx:

```bash
cd /opt/xml_parser/frontend
rm -f vite.config.js
find src -name "*.js" -o -name "*.jsx" | xargs -r rm -f
rm -f src/styles.css && rm -rf src/pages
```

Node и Python на сервере не нужны: фронтенд собирается внутри образа (`npm ci` по
lock-файлу, затем `tsc --noEmit && vite build`). Ошибка типов или сборки останавливает
`docker compose build`, поэтому битая версия до контейнеров не доходит — старые
контейнеры продолжают работать, пока новый образ не собрался.

Схема БД создаётся приложением при старте; миграций пока нет, поэтому изменения
структуры таблиц (если появятся) придётся применять руками — смотрите заметки к версии.

## 10. Эксплуатация

```bash
cd /opt/xml_parser
P="-f docker-compose.yml -f docker-compose.prod.yml"

docker compose $P ps                  # состояние
docker compose $P logs -f backend     # логи API
docker compose $P logs -f frontend    # логи nginx (кто заходил)
docker compose $P restart backend     # перезапуск API
docker compose $P down                # остановить всё (данные остаются)
docker compose $P exec db psql -U xml -d xml_parser   # консоль БД
```

Мониторинг: `https://<сервер>:9443/healthz` отдаёт `ok`, `https://<сервер>:9443/api/health` —
`{"status":"ok"}`. Неудачные входы видны в логах backend по слову «Неудачный вход».

Место на диске: `docker system df -v | grep xml_parser`.

## 11. Если что-то не работает

| Симптом | Что смотреть |
|---|---|
| `https://сервер:9080` не открывается после первого входа | Это HSTS: браузер подставил `https` к порту 9080. Заходите по `https://сервер:9443` |
| Сайт не открывается | `docker compose $P ps` — все ли контейнеры `Up`; занят ли порт: `sudo ss -tlnp \| grep -E ':9080\|:9443'` |
| `502 Bad Gateway` | backend не поднялся: `docker compose $P logs backend`; чаще всего недоступна БД |
| Браузер ругается на сертификат | самоподписанный не добавлен в доверенные, либо в `subjectAltName` не тот адрес |
| «Слишком много попыток входа» | сработала защита от подбора: 10 ошибок за 5 минут с одного адреса, подождать или `docker compose $P restart backend` |
| Все разлогинились после перезапуска | в `.env` не задан `SECRET_KEY` — он сгенерировался временным |
| Файл не грузится, 413 | размер больше `MAX_UPLOAD_MB`; поднять значение в `.env` и `client_max_body_size` в `deploy/nginx-tls.conf` |
| Файл грузится, но статус `error` | XML не того формата: сообщение видно в списке файлов в админке |
| Сборка падает: `Could not find a declaration file for module '@/api'` | на сервере остались файлы прошлой версии; см. «Важно про доставку файлов» в разделе 9 |

## 12. Windows Server

Работает так же, но с оговорками:

- нужен Docker Desktop с включённым WSL2 (или Docker CE внутри WSL2-дистрибутива);
- команды выполняйте в PowerShell, пути пишите через `/` внутри compose-файлов;
- проект держите **в файловой системе WSL** (`\\wsl$\Ubuntu\opt\xml_parser`), а не на диске
  `C:` — иначе тома и права работают заметно медленнее;
- порты 80/443 на Windows часто заняты службой IIS или `http.sys`; мы по умолчанию
  слушаем 9080/9443, но проверить занятость стоит: `netstat -ano | findstr ":9080"`;
- автозапуск: в настройках Docker Desktop включите «Start Docker Desktop when you log in»
  и настройте автологин служебной учётки, либо разворачивайте через Docker CE в WSL
  с systemd-юнитом;
- скрипт бэкапа перепишите на PowerShell либо запускайте его внутри WSL по расписанию.

## 13. Чек-лист перед вводом в эксплуатацию

- [ ] `SECRET_KEY`, `POSTGRES_PASSWORD`, `ADMIN_PASSWORD` заданы своими значениями;
- [ ] `chmod 600 .env` и `deploy/certs/server.key`;
- [ ] запуск с `docker-compose.prod.yml` (порт API наружу не опубликован);
- [ ] HTTPS работает, сертификат принимается браузерами пользователей;
- [ ] пароль администратора сменён после первого входа;
- [ ] заведены рабочие учётные записи, лишних нет;
- [ ] бэкап настроен и восстановление хотя бы раз проверено;
- [ ] firewall пропускает только `HTTP_PORT` и `HTTPS_PORT` из `.env`.
