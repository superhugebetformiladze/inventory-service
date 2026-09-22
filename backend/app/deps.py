"""Проверка, кто именно обратился к API.

FastAPI умеет подставлять в обработчик готовые объекты — это называется зависимости
(dependencies). Здесь их две:

* get_current_user — достаёт токен из заголовка Authorization, проверяет подпись
  и находит пользователя в базе. Важно: роль и признак «активен» берутся из базы,
  а не из токена, поэтому отключённый сотрудник теряет доступ немедленно, не дожидаясь,
  пока его токен протухнет.
* require_admin — то же самое плюс проверка, что у пользователя роль администратора.

Достаточно написать в обработчике `user = Depends(require_admin)`, и без прав админа
запрос до кода просто не дойдёт.
"""

from fastapi import Depends, HTTPException, status
from fastapi.security import OAuth2PasswordBearer
from sqlalchemy import select
from sqlalchemy.orm import Session

from .database import get_db
from .models import User
from .security import decode_token

oauth2_scheme = OAuth2PasswordBearer(tokenUrl="/api/auth/login")


def get_current_user(
    token: str = Depends(oauth2_scheme),
    db: Session = Depends(get_db),
) -> User:
    credentials_error = HTTPException(
        status_code=status.HTTP_401_UNAUTHORIZED,
        detail="Требуется авторизация",
        headers={"WWW-Authenticate": "Bearer"},
    )
    payload = decode_token(token)
    if not payload or not payload.get("sub"):
        raise credentials_error

    user = db.scalar(select(User).where(User.username == payload["sub"]))
    if user is None or not user.is_active:
        raise credentials_error
    return user


def require_admin(user: User = Depends(get_current_user)) -> User:
    if not user.is_admin:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Недостаточно прав")
    return user
