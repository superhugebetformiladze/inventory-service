"""Подключение к базе данных PostgreSQL.

Здесь создаётся «движок» (engine) — объект, который держит соединения с базой,
и «сессия» (Session) — через неё код читает и пишет данные. Функция get_db выдаёт
сессию на время одного запроса к API и гарантированно закрывает её после ответа,
чтобы соединения не утекали.

Base — общий предок всех таблиц: от него наследуются классы в models.py.
"""

from collections.abc import Iterator

from sqlalchemy import create_engine
from sqlalchemy.orm import DeclarativeBase, Session, sessionmaker

from .config import settings

engine = create_engine(settings.database_url, pool_pre_ping=True, future=True)
SessionLocal = sessionmaker(bind=engine, autoflush=False, expire_on_commit=False)


class Base(DeclarativeBase):
    pass


def get_db() -> Iterator[Session]:
    db = SessionLocal()
    try:
        yield db
    finally:
        db.close()
