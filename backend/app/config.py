"""Настройки приложения.

Все параметры (адрес базы, ключ подписи, лимиты) читаются из переменных окружения,
то есть из файла .env рядом с docker-compose.yml. В коде ничего менять не нужно:
поменяли значение в .env, перезапустили контейнер — настройка применилась.

Здесь же страховка: если ключ подписи не задали, приложение не станет работать
с общеизвестным значением из примера, а придумает случайный и предупредит в логе.
"""

import logging
import secrets
from functools import lru_cache
from pathlib import Path

from pydantic_settings import BaseSettings

logger = logging.getLogger("xml_parser.config")

DEFAULT_SECRET = "change-me-in-production"


class Settings(BaseSettings):
    database_url: str = "postgresql+psycopg://xml:xml@db:5432/xml_parser"
    secret_key: str = DEFAULT_SECRET
    access_token_ttl_minutes: int = 60 * 12
    storage_dir: Path = Path("/data/uploads")
    max_upload_mb: int = 30

    # Источники, которым разрешён доступ к API из браузера. По умолчанию пусто:
    # фронт ходит через тот же origin, /api проксирует nginx.
    cors_origins: list[str] = []

    # Swagger и OpenAPI-схема. На проде удобнее выключить: список эндпоинтов
    # и структура запросов — подсказка для того, кто ищет слабые места.
    enable_docs: bool = True

    # Защита от подбора пароля: сколько попыток на связку логин+IP за окно
    login_attempts: int = 10
    login_window_seconds: int = 300

    admin_username: str = "admin"
    admin_password: str = "admin"

    class Config:
        env_file = ".env"
        env_prefix = ""

    @property
    def admin_password_is_default(self) -> bool:
        return self.admin_password == "admin"


@lru_cache
def get_settings() -> Settings:
    settings = Settings()
    settings.storage_dir.mkdir(parents=True, exist_ok=True)

    if not settings.secret_key.strip() or settings.secret_key == DEFAULT_SECRET:
        # Ключ из примера конфига известен всем — с ним можно подделать любой токен.
        # Пока свой не задан, работаем на случайном: он живёт до перезапуска процесса.
        settings.secret_key = secrets.token_urlsafe(48)
        logger.warning(
            "SECRET_KEY не задан — сгенерирован временный ключ. Укажите SECRET_KEY в .env, "
            "иначе при каждом перезапуске все пользователи будут разлогинены."
        )
    elif len(settings.secret_key) < 32:
        logger.warning("SECRET_KEY короче 32 символов — подпись токенов легче подобрать.")

    return settings


settings = get_settings()
