"""Карточки оборудования: паспорта станков.

В XML-выгрузке о станке известен только его технический идентификатор. Наименование,
модель, инвентарный номер, IP и прочее приходят отдельной таблицей Excel — эти адреса
позволяют её загрузить, поправить любое поле вручную и связать карточку с конкретным
станком из мониторинга.

Смотреть карточки может любой пользователь, менять — только администратор.
"""

from fastapi import APIRouter, Depends, File, HTTPException, UploadFile, status
from sqlalchemy import select
from sqlalchemy.orm import Session

from ..config import settings
from ..database import get_db
from ..deps import get_current_user, require_admin
from ..models import EquipmentCard, MonitoringRecord, User
from ..schemas import EquipmentCreate, EquipmentOut, EquipmentUpdate
from ..services import equipment
from ..services.equipment import ExcelImportError

router = APIRouter(prefix="/api/equipment", tags=["equipment"])

EDITABLE = (
    "inv_number",
    "mdm_code",
    "monitoring_id",
    "ip_address",
    "cnc_name",
    "cnc_type",
    "shop_number",
    "workshop",
    "name",
    "model",
    "cnc_system",
    "display_name",
    "serial_number",
    "manufacturer",
    "year_made",
    "location",
    "responsible",
    "commissioned_on",
    "status",
    "notes",
    "import_note",
)


def to_out(card: EquipmentCard, monitored: set[str]) -> EquipmentOut:
    payload = {column.name: getattr(card, column.name) for column in EquipmentCard.__table__.columns}
    payload["title"] = card.title
    payload["has_data"] = bool(card.machine_key and card.machine_key in monitored)
    return EquipmentOut.model_validate(payload)


def monitored_keys(db: Session) -> set[str]:
    return set(db.scalars(select(MonitoringRecord.machine_key).distinct()))


@router.get("", response_model=list[EquipmentOut])
def list_equipment(
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[EquipmentOut]:
    monitored = monitored_keys(db)
    cards = db.scalars(
        select(EquipmentCard).order_by(EquipmentCard.row_no, EquipmentCard.inv_number)
    )
    return [to_out(card, monitored) for card in cards]


@router.get("/machines")
def machines_state(
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[dict]:
    """Станки из мониторинга и привязанные к ним карточки — для выпадающих списков."""
    monitored = sorted(monitored_keys(db), key=equipment.machine_suffix)
    bound = {
        card.machine_key: card
        for card in db.scalars(select(EquipmentCard).where(EquipmentCard.machine_key.is_not(None)))
    }
    return [
        {
            "machine_key": key,
            "mdm_code": key.split("&")[0],
            "card_id": bound[key].id if key in bound else None,
            "title": bound[key].title if key in bound else key,
            "binding_kind": bound[key].binding_kind if key in bound else "none",
        }
        for key in monitored
    ]


@router.post("", response_model=EquipmentOut, status_code=status.HTTP_201_CREATED)
def create_card(
    payload: EquipmentCreate,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> EquipmentOut:
    if db.scalar(select(EquipmentCard).where(EquipmentCard.inv_number == payload.inv_number)):
        raise HTTPException(status_code=409, detail="Карточка с таким инв. номером уже есть")

    card = EquipmentCard(**payload.model_dump(exclude_none=True))
    db.add(card)
    db.commit()
    db.refresh(card)
    return to_out(card, monitored_keys(db))


@router.patch("/{card_id}", response_model=EquipmentOut)
def update_card(
    card_id: int,
    payload: EquipmentUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> EquipmentOut:
    card = db.get(EquipmentCard, card_id)
    if card is None:
        raise HTTPException(status_code=404, detail="Карточка не найдена")

    data = payload.model_dump(exclude_unset=True)

    if "inv_number" in data and data["inv_number"] != card.inv_number:
        if not data["inv_number"]:
            raise HTTPException(status_code=400, detail="Инвентарный номер обязателен")
        exists = db.scalar(
            select(EquipmentCard).where(EquipmentCard.inv_number == data["inv_number"])
        )
        if exists:
            raise HTTPException(status_code=409, detail="Такой инв. номер уже занят")

    if "machine_key" in data:
        key = (data.pop("machine_key") or "").strip() or None
        if key:
            if key not in monitored_keys(db):
                raise HTTPException(status_code=400, detail="Такого станка нет в данных мониторинга")
            taken = db.scalar(
                select(EquipmentCard).where(
                    EquipmentCard.machine_key == key, EquipmentCard.id != card.id
                )
            )
            if taken:
                raise HTTPException(
                    status_code=409,
                    detail="Станок уже привязан к карточке {0}".format(taken.inv_number),
                )
        card.machine_key = key
        card.binding_kind = "manual" if key else "none"

    for name, value in data.items():
        if name in EDITABLE:
            setattr(card, name, value)

    db.commit()
    db.refresh(card)
    return to_out(card, monitored_keys(db))


@router.delete("/{card_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_card(
    card_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> None:
    card = db.get(EquipmentCard, card_id)
    if card is None:
        raise HTTPException(status_code=404, detail="Карточка не найдена")
    db.delete(card)
    db.commit()


@router.post("/import")
def import_equipment(
    file: UploadFile = File(...),
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> dict:
    name = (file.filename or "").lower()
    if not name.endswith((".xlsx", ".xlsm")):
        raise HTTPException(status_code=400, detail="Ожидается файл Excel (.xlsx)")

    limit = settings.max_upload_mb * 1024 * 1024
    content = file.file.read(limit + 1)
    if len(content) > limit:
        raise HTTPException(
            status_code=413, detail="Файл больше {0} МБ".format(settings.max_upload_mb)
        )

    try:
        report = equipment.import_excel(db, content)
    except ExcelImportError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    return report.as_dict()


@router.post("/rebind")
def rebind_equipment(
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> dict:
    """Пересобрать автоматические привязки (после загрузки новой выгрузки)."""
    report = equipment.rebind(db)
    db.commit()
    return report.as_dict()
