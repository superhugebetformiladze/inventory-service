"""Пароли и токены входа.

Пароли не хранятся в открытом виде: от пароля считается хеш (bcrypt) — необратимая
свёртка, по которой нельзя восстановить исходную строку. При входе сравниваются хеши.

После успешного входа выдаётся токен JWT — строка, подписанная секретным ключом
сервера. Клиент прикладывает её к каждому запросу вместо логина и пароля. Подделать
токен без ключа нельзя, а изменить срок действия или роль внутри него — тем более:
подпись сразу перестанет сходиться.
"""

import secrets
from datetime import datetime, timedelta, timezone

import bcrypt
import jwt

from .config import settings

ALGORITHM = "HS256"

# bcrypt обрабатывает только первые 72 байта и на более длинном пароле падает
BCRYPT_MAX_BYTES = 72


def _prepare(password: str) -> bytes:
    return password.encode("utf-8")[:BCRYPT_MAX_BYTES]


def hash_password(password: str) -> str:
    return bcrypt.hashpw(_prepare(password), bcrypt.gensalt()).decode("utf-8")


def verify_password(password: str, hashed: str) -> bool:
    try:
        return bcrypt.checkpw(_prepare(password), hashed.encode("utf-8"))
    except ValueError:
        return False


_DUMMY_HASH = bcrypt.hashpw(b"timing-attack-guard", bcrypt.gensalt())


def dummy_verify() -> None:
    """Съедает то же время, что и реальная проверка, — чтобы по скорости ответа
    нельзя было отличить существующий логин от несуществующего."""
    bcrypt.checkpw(b"timing-attack-guard-x", _DUMMY_HASH)


def create_access_token(subject: str, role: str) -> str:
    now = datetime.now(timezone.utc)
    payload = {
        "sub": subject,
        "role": role,
        "iat": now,
        "exp": now + timedelta(minutes=settings.access_token_ttl_minutes),
        "jti": secrets.token_hex(8),
    }
    return jwt.encode(payload, settings.secret_key, algorithm=ALGORITHM)


def decode_token(token: str) -> dict | None:
    try:
        return jwt.decode(token, settings.secret_key, algorithms=[ALGORITHM])
    except jwt.PyJWTError:
        return None
