.DEFAULT_GOAL := help

help: ## Список команд
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

up: ## Поднять стек
	docker compose up -d --build

down: ## Остановить стек
	docker compose down

reset: ## Остановить и удалить данные (БД + загруженные файлы)
	docker compose down -v

logs: ## Логи backend
	docker compose logs -f backend

ps: ## Состояние контейнеров
	docker compose ps

shell: ## Консоль в контейнере backend
	docker compose exec backend bash

psql: ## Консоль PostgreSQL
	docker compose exec db psql -U xml -d xml_parser

typecheck: ## Проверить типы фронтенда (без установки node локально)
	docker run --rm -v "$$PWD/frontend:/app" -w /app node:22-alpine 		sh -c "npm ci --silent && npx tsc --noEmit"

PROD := -f docker-compose.yml -f docker-compose.prod.yml

prod-up: ## Поднять боевую конфигурацию (HTTPS, порт API закрыт)
	docker compose $(PROD) up -d --build

prod-down: ## Остановить боевую конфигурацию
	docker compose $(PROD) down

prod-ps: ## Состояние боевой конфигурации
	docker compose $(PROD) ps

prod-logs: ## Логи боевого backend
	docker compose $(PROD) logs -f backend

backup: ## Дамп БД и архив загруженных файлов в ./backups
	mkdir -p backups
	docker compose $(PROD) exec -T db pg_dump -U xml xml_parser | gzip > backups/db-$$(date +%F).sql.gz
	docker run --rm -v xml_parser_uploads:/data:ro -v "$$PWD/backups:/backup" alpine tar czf /backup/uploads-$$(date +%F).tar.gz -C /data .
	@echo "Готово: backups/db-$$(date +%F).sql.gz и backups/uploads-$$(date +%F).tar.gz"
