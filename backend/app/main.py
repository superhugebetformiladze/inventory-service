"""Сборка и запуск приложения.

Этот файл — точка входа: он создаёт приложение FastAPI, подключает к нему разделы API
(вход, пользователи, файлы, отчёты, оборудование) и выполняет действия при старте:

  1. дожидается, пока поднимется база (в docker-compose она стартует параллельно);
  2. создаёт недостающие таблицы и колонки;
  3. заводит первого администратора, если база пустая.

Здесь же ограничение на размер входящего запроса — защита от того, чтобы кто-то
занял всю память сервера одним гигантским запросом ещё до проверки прав.
"""

import logging
import time
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from sqlalchemy import select, text
from sqlalchemy.exc import OperationalError

from .config import settings
from .database import Base, SessionLocal, engine
from .models import EquipmentCard, User
from .routers import auth, data, equipment, files, users
from .security import hash_password

logger = logging.getLogger("xml_parser")


@asynccontextmanager
async def lifespan(_: FastAPI):
    """Что происходит при запуске приложения, до приёма первого запроса."""
    wait_for_db()
    Base.metadata.create_all(engine)
    ensure_columns()
    migrate_aliases()
    ensure_admin()
    yield


app = FastAPI(
    lifespan=lifespan,
    title="XML Parser — мониторинг оборудования",
    version="1.0.0",
    description="Разбор XML-выгрузок системы мониторинга станков и аналитика по ним.",
    docs_url="/docs" if settings.enable_docs else None,
    redoc_url="/redoc" if settings.enable_docs else None,
    openapi_url="/openapi.json" if settings.enable_docs else None,
)

# Фронт ходит через тот же origin (nginx проксирует /api), поэтому по умолчанию
# сторонним сайтам обращаться к API незачем. Чужие origin'ы задаются CORS_ORIGINS.
if settings.cors_origins:
    app.add_middleware(
        CORSMiddleware,
        allow_origins=settings.cors_origins,
        allow_credentials=False,
        allow_methods=["GET", "POST", "PATCH", "PUT", "DELETE"],
        allow_headers=["Authorization", "Content-Type"],
    )

UPLOAD_PATH = "/api/files/upload"
SMALL_BODY_LIMIT = 1024 * 1024  # обычным JSON-запросам мегабайта хватает с запасом


@app.middleware("http")
async def limit_body_size(request: Request, call_next):
    """Отсекаем гигантские тела до того, как их прочитает приложение.

    nginx ограничивает размер сам, но API может быть опубликован напрямую —
    тогда без этой проверки любой желающий отправит многогигабайтный запрос
    ещё до авторизации и выест память процесса.
    """
    declared = request.headers.get("content-length")
    if declared and declared.isdigit():
        allowed = (
            settings.max_upload_mb * 1024 * 1024 + 2 * 1024 * 1024
            if request.url.path.startswith(UPLOAD_PATH)
            else SMALL_BODY_LIMIT
        )
        if int(declared) > allowed:
            return JSONResponse(
                status_code=413,
                content={"detail": "Тело запроса слишком большое"},
            )
    return await call_next(request)


app.include_router(auth.router)
app.include_router(users.router)
app.include_router(files.router)
app.include_router(data.router)
app.include_router(equipment.router)


def wait_for_db(attempts: int = 30, delay: float = 2.0) -> None:
    for attempt in range(1, attempts + 1):
        try:
            with engine.connect() as conn:
                conn.exec_driver_sql("SELECT 1")
            return
        except OperationalError:
            logger.warning("БД недоступна, попытка %s/%s", attempt, attempts)
            time.sleep(delay)
    raise RuntimeError("Не удалось подключиться к базе данных")


NEW_COLUMNS = {
    "monitoring_id": "INTEGER",
    "ip_address": "VARCHAR(64)",
    "cnc_name": "VARCHAR(128)",
    "cnc_type": "VARCHAR(255)",
    "shop_number": "VARCHAR(32)",
}


def ensure_columns() -> None:
    """Alembic не подключён: новые поля карточек добавляем сами, идемпотентно."""
    with SessionLocal() as db:
        for column, kind in NEW_COLUMNS.items():
            db.execute(
                text(
                    "ALTER TABLE equipment_cards ADD COLUMN IF NOT EXISTS {0} {1}".format(
                        column, kind
                    )
                )
            )
        db.execute(
            text(
                "CREATE INDEX IF NOT EXISTS ix_equipment_cards_monitoring_id "
                "ON equipment_cards (monitoring_id)"
            )
        )
        db.commit()


def migrate_aliases() -> None:
    """Названия станков раньше жили в machine_aliases, теперь — в карточках оборудования."""
    with SessionLocal() as db:
        exists = db.execute(
            text("SELECT to_regclass('machine_aliases') IS NOT NULL")
        ).scalar()
        if not exists:
            return
        rows = db.execute(text("SELECT machine_key, name FROM machine_aliases")).all()
        for machine_key, name in rows:
            card = db.scalar(
                select(EquipmentCard).where(EquipmentCard.machine_key == machine_key)
            )
            if card is None:
                db.add(
                    EquipmentCard(
                        inv_number="alias-{0}".format(machine_key),
                        machine_key=machine_key,
                        display_name=name,
                        binding_kind="manual",
                    )
                )
            elif not card.display_name:
                card.display_name = name
        db.execute(text("DROP TABLE machine_aliases"))
        db.commit()
        logger.info("Перенесено названий станков из machine_aliases: %s", len(rows))


def ensure_admin() -> None:
    with SessionLocal() as db:
        if db.scalar(select(User).where(User.role == "admin")):
            return
        db.add(
            User(
                username=settings.admin_username,
                full_name="Администратор",
                role="admin",
                hashed_password=hash_password(settings.admin_password),
            )
        )
        db.commit()
        logger.info("Создан администратор по умолчанию: %s", settings.admin_username)
        if settings.admin_password_is_default:
            logger.warning(
                "Администратор %s создан с паролем по умолчанию — смените его "
                "в разделе «Пользователи» сразу после первого входа.",
                settings.admin_username,
            )


@app.get("/api/health")
def health() -> dict:
    return {"status": "ok"}
