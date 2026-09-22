"""Управление пользователями (доступно только администраторам).

Создание, изменение, отключение и удаление учётных записей. Роли всего две:
«admin» — может загружать файлы и заводить людей, «viewer» — только смотрит отчёты.

Отдельная страховка: система не должна остаться без администратора. Поэтому нельзя
удалить себя, а также снять права, отключить или удалить последнего действующего админа —
иначе некому будет заводить пользователей и загружать выгрузки.
"""

from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from ..database import get_db
from ..deps import require_admin
from ..models import User
from ..schemas import UserCreate, UserOut, UserUpdate
from ..security import hash_password

router = APIRouter(prefix="/api/users", tags=["users"], dependencies=[Depends(require_admin)])

ROLES = ("admin", "viewer")


@router.get("", response_model=list[UserOut])
def list_users(db: Session = Depends(get_db)) -> list[User]:
    return list(db.scalars(select(User).order_by(User.id)))


@router.post("", response_model=UserOut, status_code=status.HTTP_201_CREATED)
def create_user(payload: UserCreate, db: Session = Depends(get_db)) -> User:
    if payload.role not in ROLES:
        raise HTTPException(status_code=400, detail="Роль должна быть admin или viewer")
    if db.scalar(select(User).where(User.username == payload.username)):
        raise HTTPException(status_code=409, detail="Такой пользователь уже есть")

    user = User(
        username=payload.username,
        full_name=payload.full_name,
        role=payload.role,
        hashed_password=hash_password(payload.password),
    )
    db.add(user)
    db.commit()
    db.refresh(user)
    return user


def other_active_admins(db: Session, user_id: int) -> int:
    """Сколько останется действующих администраторов, кроме этого пользователя."""
    return (
        db.scalar(
            select(func.count())
            .select_from(User)
            .where(User.role == "admin", User.is_active.is_(True), User.id != user_id)
        )
        or 0
    )


def ensure_not_last_admin(db: Session, user: User, action: str) -> None:
    """Иначе можно снять с себя права или отключить единственного админа —
    и система останется без учётки, способной заводить пользователей."""
    if user.role == "admin" and user.is_active and other_active_admins(db, user.id) == 0:
        raise HTTPException(
            status_code=400,
            detail="Это единственный действующий администратор — {0} нельзя. "
            "Сначала назначьте другого администратора.".format(action),
        )


@router.patch("/{user_id}", response_model=UserOut)
def update_user(user_id: int, payload: UserUpdate, db: Session = Depends(get_db)) -> User:
    user = db.get(User, user_id)
    if user is None:
        raise HTTPException(status_code=404, detail="Пользователь не найден")

    if payload.role is not None:
        if payload.role not in ROLES:
            raise HTTPException(status_code=400, detail="Роль должна быть admin или viewer")
        if payload.role != "admin":
            ensure_not_last_admin(db, user, "снять с него права администратора")
        user.role = payload.role
    if payload.full_name is not None:
        user.full_name = payload.full_name
    if payload.is_active is not None:
        if not payload.is_active:
            ensure_not_last_admin(db, user, "отключить его")
        user.is_active = payload.is_active
    if payload.password:
        user.hashed_password = hash_password(payload.password)

    db.commit()
    db.refresh(user)
    return user


@router.delete("/{user_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_user(
    user_id: int,
    db: Session = Depends(get_db),
    current: User = Depends(require_admin),
) -> None:
    user = db.get(User, user_id)
    if user is None:
        raise HTTPException(status_code=404, detail="Пользователь не найден")
    if user.id == current.id:
        raise HTTPException(status_code=400, detail="Нельзя удалить самого себя")
    ensure_not_last_admin(db, user, "удалить его")
    db.delete(user)
    db.commit()
