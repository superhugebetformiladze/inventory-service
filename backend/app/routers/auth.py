"""Вход в систему.

Один основной адрес: POST /api/auth/login — принимает логин и пароль, отдаёт токен.

Защита от подбора пароля: считаем неудачные попытки по паре «логин + адрес клиента».
После десяти ошибок за пять минут отвечаем кодом 429 и просим подождать. Реальный адрес
берём из заголовка X-Forwarded-For, но только если запрос пришёл от нашего nginx
из внутренней сети, иначе такой заголовок легко подделать.

Ещё одна мелочь: при несуществующем логине мы всё равно тратим время на проверку хеша.
Иначе по скорости ответа можно было бы понять, какие логины заведены в системе.
"""

import logging

from fastapi import APIRouter, Depends, HTTPException, Request, status
from fastapi.security import OAuth2PasswordRequestForm
from sqlalchemy import select
from sqlalchemy.orm import Session

from .. import ratelimit
from ..database import get_db
from ..deps import get_current_user
from ..models import User
from ..schemas import Token, UserOut
from ..security import create_access_token, dummy_verify, verify_password

logger = logging.getLogger("xml_parser.auth")
router = APIRouter(prefix="/api/auth", tags=["auth"])


PRIVATE_PREFIXES = ("127.", "10.", "192.168.", "172.16.", "172.17.", "172.18.",
                    "172.19.", "172.20.", "172.21.", "172.22.", "172.23.", "172.24.",
                    "172.25.", "172.26.", "172.27.", "172.28.", "172.29.", "172.30.",
                    "172.31.", "::1")


def client_ip(request: Request) -> str:
    """Реальный адрес клиента. Запросы приходят через nginx, поэтому у всех
    одинаковый peer-адрес контейнера — берём X-Forwarded-For, но только если
    сосед действительно наш прокси из приватной сети (иначе заголовок подделают)."""
    peer = request.client.host if request.client else "unknown"
    forwarded = request.headers.get("x-forwarded-for")
    if forwarded and peer.startswith(PRIVATE_PREFIXES):
        return forwarded.split(",")[0].strip() or peer
    return peer


def client_key(request: Request, username: str) -> str:
    return "{0}|{1}".format(client_ip(request), username.lower())


@router.post("/login", response_model=Token)
def login(
    request: Request,
    form: OAuth2PasswordRequestForm = Depends(),
    db: Session = Depends(get_db),
) -> Token:
    key = client_key(request, form.username)
    if ratelimit.is_blocked(key):
        raise HTTPException(
            status_code=status.HTTP_429_TOO_MANY_REQUESTS,
            detail="Слишком много попыток входа. Попробуйте позже.",
            headers={"Retry-After": str(ratelimit.retry_after(key))},
        )

    invalid = HTTPException(
        status_code=status.HTTP_401_UNAUTHORIZED,
        detail="Неверный логин или пароль",
    )

    user = db.scalar(select(User).where(User.username == form.username))
    if user is None:
        dummy_verify()  # отвечаем за то же время, что и при существующем логине
        ratelimit.register_failure(key)
        logger.warning("Неудачный вход: пользователь %r не найден", form.username)
        raise invalid

    if not verify_password(form.password, user.hashed_password):
        ratelimit.register_failure(key)
        logger.warning("Неудачный вход: неверный пароль для %r", form.username)
        raise invalid

    if not user.is_active:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Пользователь отключён")

    ratelimit.reset(key)
    return Token(access_token=create_access_token(user.username, user.role))


@router.get("/me", response_model=UserOut)
def me(user: User = Depends(get_current_user)) -> User:
    return user
