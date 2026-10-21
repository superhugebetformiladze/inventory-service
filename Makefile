# Единственная точка входа. Всё — тесты, линтер, сборка — идёт через docker
# compose: локальная установка Go не должна влиять на результат.

.DEFAULT_GOAL := help

COMPOSE = docker compose --env-file .env -f deploy/docker-compose.yml
# Разовые команды не поднимают зависимости: линтеру и `go build` база не нужна,
# а ждать её healthcheck ради gofmt — минута на пустом месте.
RUN     = $(COMPOSE) run --rm --no-deps server

.PHONY: help
help: ## Показать эти команды
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: env
env: ## Создать .env из образца, если его ещё нет
	@test -f .env || (cp .env.example .env && echo "Создан .env — задайте в нём POSTGRES_PASSWORD")

.PHONY: dev
dev: env ## Поднять окружение разработки
	$(COMPOSE) up -d --build

.PHONY: restart
restart: ## Перезапустить службу после правки кода
	# Именно restart, а не `up -d --build`: код монтируется томом, образ при
	# правке не меняется, и compose не пересоздаёт контейнер — служба
	# продолжает крутить то, что скомпилировала при старте.
	$(COMPOSE) restart server

.PHONY: down
down: ## Остановить окружение
	$(COMPOSE) down

.PHONY: clean
clean: ## Остановить и удалить тома — данные будут потеряны
	$(COMPOSE) down -v

.PHONY: status
status: ## Состояние сервисов
	$(COMPOSE) ps

.PHONY: logs
logs: ## Журнал службы
	$(COMPOSE) logs -f server

.PHONY: test
test: ## Тесты
	$(RUN) go test ./... -race -count=1

.PHONY: cover
cover: ## Тесты с отчётом о покрытии
	$(RUN) sh -c 'go test ./... -coverprofile=/tmp/c.out && go tool cover -func=/tmp/c.out | tail -1'

.PHONY: lint
lint: ## Линтер
	$(RUN) golangci-lint run ./...

.PHONY: fmt
fmt: ## Форматирование
	$(RUN) gofmt -w -l .

.PHONY: vet
vet: ## Проверки компилятора
	$(RUN) go vet ./...

.PHONY: tidy
tidy: ## Привести go.mod и go.sum в порядок
	$(RUN) go mod tidy

.PHONY: migrate
migrate: ## Накатить миграции базы
	$(COMPOSE) run --rm server go run ./cmd/migrate

.PHONY: topics
topics: ## Создать топики ленты с нужными настройками
	$(COMPOSE) exec redpanda rpk topic create inventory.snapshots.v1 -p 3 -c retention.ms=2592000000 || true
	$(COMPOSE) exec redpanda rpk topic create inventory.hosts.v1     -p 3 -c cleanup.policy=compact || true
	$(COMPOSE) exec redpanda rpk topic create inventory.events.v1    -p 3 -c retention.ms=7776000000 || true
	$(COMPOSE) exec redpanda rpk topic create inventory.network.v1   -p 3 -c cleanup.policy=compact || true
	$(COMPOSE) exec redpanda rpk topic list

.PHONY: token
token: ## Создать токен регистрации агентов (печатается один раз)
	$(COMPOSE) run --rm server go run ./cmd/agentctl token -label "$(LABEL)" -days $(or $(DAYS),14) -uses $(or $(USES),0)

.PHONY: agents
agents: ## Список агентов: кто на связи, кто отозван
	$(COMPOSE) run --rm server go run ./cmd/agentctl list

.PHONY: revoke
revoke: ## Отозвать ключ машины: make revoke AGENT=<отметка> REASON="..."
	$(COMPOSE) run --rm server go run ./cmd/agentctl revoke -agent "$(AGENT)" -reason "$(REASON)"

.PHONY: collect
collect: ## Собрать сейчас: make collect HOST=<имя компьютера>
	$(COMPOSE) run --rm server go run ./cmd/agentctl collect -host "$(HOST)"

.PHONY: tasks
tasks: ## Очередь заданий и их состояние
	$(COMPOSE) run --rm server go run ./cmd/agentctl commands

.PHONY: pending
pending: ## Очередь разбора: машины, которые не удалось опознать
	$(COMPOSE) run --rm server go run ./cmd/agentctl pending

.PHONY: link
link: ## Связать отложенный отчёт с машиной: make link REPORT=<id> HOST=<id>
	$(COMPOSE) run --rm server go run ./cmd/agentctl link -report "$(REPORT)" -host "$(HOST)"

.PHONY: link-new
link-new: ## Завести новую машину по отложенному отчёту: make link-new REPORT=<id>
	$(COMPOSE) run --rm server go run ./cmd/agentctl link -report "$(REPORT)" -new

.PHONY: import
import: ## Принять отчёты с машин без сети: make import DIR=/mnt/флешка
	$(COMPOSE) run --rm -v "$(DIR):/mnt/offline:ro" server go run ./cmd/agentctl import -dir /mnt/offline

.PHONY: netscan-import
netscan-import: ## Принять снимки коммутаторов из файлов (.walk/.json): make netscan-import DIR=/путь
	$(COMPOSE) run --rm -v "$(DIR):/mnt/netscan:ro" server go run ./cmd/agentctl netscan-import -dir /mnt/netscan

.PHONY: netscan-poll
netscan-poll: ## Опросить коммутатор сейчас: make netscan-poll TARGET=192.168.4.172 [DRY=1]
	$(COMPOSE) run --rm server go run ./cmd/agentctl netscan-poll -target "$(TARGET)" $(if $(filter 1,$(DRY)),-dry,)

.PHONY: netscan-list
netscan-list: ## Опрошенные сетевые устройства
	$(COMPOSE) run --rm server go run ./cmd/agentctl netscan-list

.PHONY: netscan-exe
netscan-exe: ## Собрать netscan.exe — опрос коммутаторов с машины, у которой есть к ним доступ
	@mkdir -p build
	$(RUN) sh -c 'GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o build/netscan.exe ./cmd/netscan'
	@cp scripts/switches.example.txt build/
	@ls -lh build/netscan.exe

.PHONY: agent
agent: ## Собрать комплект раскатки под Windows в build/
	@mkdir -p build
	$(RUN) sh -c 'GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o build/agent.exe ./cmd/agent'
	@cp deploy/windows/*.cmd build/
	@ls -lh build/

.PHONY: package
package: agent ## Собрать пакет раскатки в build/пакет/ (кладётся в общую папку)
	@rm -rf build/пакет && mkdir -p build/пакет
	@cp build/agent.exe build/пакет/
	@cp deploy/windows/*.cmd build/пакет/
	@cp deploy/windows/ЧИТАТЬ.txt build/пакет/
	@# Версия файлом, а не запуском agent.exe со share: на машинах с запретом
	@# запуска программ из сети скрипт раскатки иначе не смог бы сравнить
	@# версии и обновлял бы всё подряд на каждой загрузке.
	@$(RUN) sh -c 'go run ./cmd/agent version' | tr -d '\r' > build/пакет/version.txt
	@printf '' > build/пакет/token.txt
	@# Адрес службы одним файлом на весь пакет: держать его в двух скриптах
	@# значит однажды поправить один и раскатать половину парка не туда.
	@printf 'http://inventory:8080' > build/пакет/server.txt
	@echo "пакет собран: build/пакет (версия $$(cat build/пакет/version.txt))"
	@echo "положите в него токен раскатки: make token LABEL=... DAYS=14 USES=600"
	@ls -1 build/пакет/

.PHONY: demo
demo: ## Сквозной путь: регистрация -> отчёт -> база -> outbox -> лента
	@# Токен создаётся тут же: симулятор проходит тот же путь, что настоящий
	@# агент, — без ключа отчёт не примут.
	@#
	@# Два отчёта с каждой машины, а не один. Первый идёт путём новой машины,
	@# второй — путём уже известной, и это разный код. Проверка одним отчётом
	@# пропустила дефект, из-за которого приём отчёта от существующей машины
	@# падал сутки, пока новые машины заводились как ни в чём не бывало.
	@TOKEN=$$($(COMPOSE) run --rm -T server go run ./cmd/agentctl token -label demo -days 1 -uses 100 2>/dev/null | tail -1 | tr -d '\r'); \
	$(COMPOSE) run --rm --no-deps -T server go run ./cmd/simulator -addr http://server:8080 -token $$TOKEN -hosts 3 -rounds 2 -v
	@echo "--- ждём релей ---"
	@sleep 3
	$(COMPOSE) exec redpanda rpk topic consume inventory.snapshots.v1 -n 3 -o start -f '%k -> %v\n' | head -40

.PHONY: metrics
metrics: ## Поднять наблюдение: Prometheus и Grafana
	$(COMPOSE) --profile metrics up -d prometheus grafana
	@echo "Grafana: http://localhost:$${GRAFANA_PORT:-3001}/d/inventory"
	@echo "Prometheus: http://localhost:$${PROMETHEUS_PORT:-9090}"

.PHONY: loadtest
loadtest: ## Утренний всплеск: make loadtest HOSTS=1000
	@TOKEN=$$($(COMPOSE) run --rm -T server go run ./cmd/agentctl token -label нагрузка -days 1 -uses $(or $(HOSTS),1000) 2>/dev/null | tail -1 | tr -d '\r'); 	$(COMPOSE) run --rm --no-deps -T server go run ./cmd/simulator 		-addr http://server:8080 -token $$TOKEN 		-hosts $(or $(HOSTS),1000) -rounds 1 -jitter 60s

.PHONY: backup
backup: ## Снять копию базы и проверить её восстановлением
	@set -a; . ./.env; set +a; sh scripts/backup-verify.sh $(or $(DIR),./backups)

.PHONY: cleanup
cleanup: ## Разово удалить устаревшее
	$(COMPOSE) run --rm server go run ./cmd/agentctl cleanup

.PHONY: retire
retire: ## Вывести машину из учёта: make retire HOST=<id> | NAME=<имя> | FILE=<файл>
	$(COMPOSE) run --rm server go run ./cmd/agentctl retire $(if $(HOST),-host $(HOST),) $(if $(NAME),-name $(NAME),) $(if $(FILE),-file $(FILE),)

.PHONY: tail
tail: ## Читать ленту снимков
	$(COMPOSE) exec redpanda rpk topic consume inventory.snapshots.v1 -f '%k %v\n'

.PHONY: build
build: ## Собрать образ службы
	docker build -f deploy/Dockerfile -t inventory-service:dev .

.PHONY: ci
ci: fmt vet lint test ## Всё сразу: формат, vet, линтер, тесты

.PHONY: smoke
smoke: ## Проверить живой стек
	@echo "--- /health ---"; curl -sf http://localhost:$${HTTP_EXPOSED_PORT:-8090}/health && echo
	@echo "--- /ready  ---"; curl -sf http://localhost:$${HTTP_EXPOSED_PORT:-8090}/ready && echo

# ============================================================
# РАБОЧИЙ СЕРВЕР
# ============================================================
#
# Отдельный стек (deploy/docker-compose.prod.yml) и отдельный файл окружения
# (.env.prod). Порядок первого запуска — docs/ДЕПЛОЙ.md.

PROD = docker compose --env-file .env.prod -f deploy/docker-compose.prod.yml

# Версия образа приходит из командной строки: make prod-build APP_VERSION=2026-09-14.
# Переменная окружения сильнее значения в .env.prod — так прежнюю версию можно
# поднять, не правя файл.
export APP_VERSION

.PHONY: prod-build prod-migrate prod-up prod-down prod-ps prod-logs         prod-token prod-agents prod-ctl prod-backup prod-metrics

prod-build: ## Собрать рабочий образ: make prod-build APP_VERSION=2026-09-14
	$(PROD) build server

prod-migrate: ## Накатить миграции — до запуска новой версии, не после
	$(PROD) run --rm migrate

prod-up: ## Поднять рабочий стек и дождаться готовности
	$(PROD) up -d --wait

prod-down: ## Остановить рабочий стек (данные в томах остаются)
	$(PROD) down

prod-ps: ## Состояние контейнеров рабочего стека
	$(PROD) ps

prod-logs: ## Журнал службы
	$(PROD) logs -f --tail 200 server

prod-token: ## Токен раскатки: make prod-token LABEL="раскатка" DAYS=14 USES=600
	$(PROD) run --rm agentctl token -label "$(LABEL)" -days $(DAYS) -uses $(USES)

prod-agents: ## Список агентов: кто на связи, какая версия
	$(PROD) run --rm agentctl list

prod-ctl: ## Любая команда agentctl: make prod-ctl ARGS="retire -name PC-01"
	$(PROD) run --rm agentctl $(ARGS)

prod-backup: ## Копия базы с проверкой восстановлением
	@set -a; . ./.env.prod; set +a; COMPOSE="$(PROD)" sh scripts/backup-verify.sh $(or $(DIR),./backups)

prod-metrics: ## Поднять Prometheus и Grafana (нужен GRAFANA_ADMIN_PASSWORD)
# Пароль проверяется здесь, а не в compose: обязательная переменная в файле
# ломает и `prod-up`, потому что интерполяция идёт по всему файлу, а не по
# активному профилю. Пустое значение означало бы Grafana с admin/admin.
	@set -a; . ./.env.prod; set +a; \
	if [ -z "$${GRAFANA_ADMIN_PASSWORD:-}" ]; then \
		echo "GRAFANA_ADMIN_PASSWORD не задан в .env.prod."; \
		echo "Без него Grafana поднимется с паролем admin/admin на открытом порту."; \
		exit 1; \
	fi
	$(PROD) --profile metrics up -d
