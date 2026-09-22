"""Отчёты: всё, что показывает клиентская часть.

Общие для всех отчётов параметры выборки собирает build_query:
  * scope=file — смотрим одну конкретную выгрузку, scope=range — диапазон дат по всем;
  * schedule_type=1 — учитываем только время рабочих смен, =2 — календарные сутки;
  * shift — номер смены, machines — список станков.

Адреса: сводка (overview), разрез по станкам, динамика по суткам, тепловая карта,
постраничный список записей, выгрузка в CSV и карточка отдельного станка.
"""

import csv
import io
from datetime import datetime

from fastapi import APIRouter, Depends, HTTPException, Query as Q
from fastapi.responses import StreamingResponse
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from ..database import get_db
from ..deps import get_current_user, require_admin
from ..models import EquipmentCard, FileParam, MonitoringRecord, SourceFile, User
from ..schemas import EquipmentOut, FiltersOut, MachineOut, ParamOut
from ..services import analytics
from ..services.analytics import Query as DataQuery

router = APIRouter(prefix="/api/data", tags=["data"])


def build_query(
    scope: str = Q("file", pattern="^(file|range)$"),
    file_id: int | None = None,
    date_from: datetime | None = None,
    date_to: datetime | None = None,
    schedule_type: int = Q(1, ge=1, le=2),
    shift: int | None = Q(None, ge=1, le=9),
    machines: list[str] | None = Q(None),
) -> DataQuery:
    if scope == "file" and file_id is None:
        raise HTTPException(status_code=400, detail="Для режима «файл» нужен file_id")
    return DataQuery(
        scope=scope,
        file_id=file_id,
        date_from=date_from,
        date_to=date_to,
        schedule_type=schedule_type,
        shift=shift,
        machines=machines or None,
    )


@router.get("/filters", response_model=FiltersOut)
def filters(
    file_id: int | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> FiltersOut:
    alias_map = analytics.aliases(db)

    machines_stmt = select(
        MonitoringRecord.machine_key,
        func.min(MonitoringRecord.machine_ext_id),
    ).group_by(MonitoringRecord.machine_key)
    params_stmt = select(
        FileParam.integration_id, FileParam.name, FileParam.color, FileParam.group_name
    ).group_by(FileParam.integration_id, FileParam.name, FileParam.color, FileParam.group_name)
    shifts_stmt = select(MonitoringRecord.smena_num).distinct()
    bounds_stmt = select(func.min(MonitoringRecord.calc_date), func.max(MonitoringRecord.calc_date))

    if file_id is not None:
        machines_stmt = machines_stmt.where(MonitoringRecord.file_id == file_id)
        params_stmt = params_stmt.where(FileParam.file_id == file_id)
        shifts_stmt = shifts_stmt.where(MonitoringRecord.file_id == file_id)
        bounds_stmt = bounds_stmt.where(MonitoringRecord.file_id == file_id)

    machines = [
        MachineOut(
            key=key,
            name=alias_map.get(key) or key,
            alias=alias_map.get(key),
        )
        for key, _ext in sorted(db.execute(machines_stmt), key=lambda row: row[1] or 0)
    ]
    params = [
        ParamOut(code=code, name=name, color=color, group_name=group)
        for code, name, color, group in db.execute(params_stmt)
    ]
    params.sort(key=lambda item: (item.group_name or "", item.code or ""))
    date_min, date_max = db.execute(bounds_stmt).one()
    shifts = sorted(value for value in db.scalars(shifts_stmt) if value)

    return FiltersOut(
        machines=machines,
        params=params,
        groups=sorted({item.group_name or "Без группы" for item in params}),
        date_min=date_min,
        date_max=date_max,
        shifts=shifts,
    )


@router.get("/overview")
def overview(
    query: DataQuery = Depends(build_query),
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> dict:
    """Сводка по выборке: итоговые часы, коэффициенты и разбивка по группам и состояниям."""
    return analytics.overview(db, query)


@router.get("/machines")
def machines(
    query: DataQuery = Depends(build_query),
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[dict]:
    """По каждому станку: сколько он работал, простаивал и был выключен."""
    return analytics.by_machine(db, query)


@router.get("/timeline")
def timeline(
    query: DataQuery = Depends(build_query),
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[dict]:
    """По каждым суткам периода: часы по состояниям и коэффициент загрузки."""
    return analytics.by_day(db, query)


@router.get("/heatmap")
def heatmap(
    query: DataQuery = Depends(build_query),
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> dict:
    """Матрица «станок × сутки» — данные для тепловой карты."""
    return analytics.heatmap(db, query)


@router.get("/records")
def records(
    query: DataQuery = Depends(build_query),
    limit: int = Q(100, ge=1, le=1000),
    offset: int = Q(0, ge=0),
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> dict:
    """Постраничный список исходных интервалов — для вкладки «Записи»."""
    return analytics.records(db, query, limit=limit, offset=offset)


def csv_safe(value: object) -> str:
    """Excel исполняет ячейки, начинающиеся с = + - @ и табуляции. Названия станков
    и состояний приходят из чужого XML, поэтому такие значения экранируем."""
    text = "" if value is None else str(value)
    dangerous = ("=", "+", "-", "@", '\t', '\r', '\n')
    return "'" + text if text[:1] in dangerous else text


@router.get("/export.csv")
def export_csv(
    query: DataQuery = Depends(build_query),
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> StreamingResponse:
    page = analytics.records(db, query, limit=100000, offset=0)
    buffer = io.StringIO()
    writer = csv.writer(buffer, delimiter=";")
    writer.writerow(
        ["Дата", "Станок", "Смена", "Группа", "Состояние", "Код", "Время, ч", "Событий"]
    )
    for row in page["items"]:
        writer.writerow(
            [
                row["calc_date"].strftime("%Y-%m-%d") if row["calc_date"] else "",
                csv_safe(row["machine"]),
                row["shift"] or "",
                csv_safe(row["group"]),
                csv_safe(row["param"]),
                csv_safe(row["code"]),
                str(round(row["seconds"] / 3600, 3)).replace(".", ","),
                row["events"],
            ]
        )
    data = "﻿" + buffer.getvalue()
    return StreamingResponse(
        iter([data]),
        media_type="text/csv; charset=utf-8",
        headers={"Content-Disposition": 'attachment; filename="monitoring.csv"'},
    )


@router.get("/periods")
def periods(
    schedule_type: int = Q(1, ge=1, le=2),
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[dict]:
    """Какие сутки и из какого файла доступны — для выбора диапазона дат."""
    rows = db.execute(
        select(
            MonitoringRecord.calc_date,
            MonitoringRecord.file_id,
            SourceFile.original_name,
            SourceFile.uploaded_at,
            func.count(MonitoringRecord.id),
        )
        .join(SourceFile, SourceFile.id == MonitoringRecord.file_id)
        .where(MonitoringRecord.schedule_type_id == schedule_type)
        .group_by(
            MonitoringRecord.calc_date,
            MonitoringRecord.file_id,
            SourceFile.original_name,
            SourceFile.uploaded_at,
        )
        .order_by(MonitoringRecord.calc_date)
    ).all()

    best: dict[datetime, dict] = {}
    for calc_date, file_id, name, uploaded_at, count in rows:
        current = best.get(calc_date)
        if current is None or (uploaded_at, file_id) > (current["uploaded_at"], current["file_id"]):
            best[calc_date] = {
                "date": calc_date,
                "file_id": file_id,
                "file_name": name,
                "uploaded_at": uploaded_at,
                "records": int(count),
            }
    return [best[key] for key in sorted(best)]


@router.get("/machine/{machine_key:path}")
def machine_card(
    machine_key: str,
    query: DataQuery = Depends(build_query),
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> dict:
    """Страница станка: паспорт из карточки + показатели за выбранный период."""
    known = db.scalar(
        select(func.count())
        .select_from(MonitoringRecord)
        .where(MonitoringRecord.machine_key == machine_key)
    )
    if not known:
        raise HTTPException(status_code=404, detail="Станок не найден")

    detail = analytics.machine_detail(db, query, machine_key)
    card = db.scalar(select(EquipmentCard).where(EquipmentCard.machine_key == machine_key))
    if card is not None:
        payload = {
            column.name: getattr(card, column.name) for column in EquipmentCard.__table__.columns
        }
        payload["title"] = card.title
        payload["has_data"] = True
        detail["card"] = EquipmentOut.model_validate(payload)
    else:
        detail["card"] = None
    detail["title"] = card.title if card is not None else machine_key
    return detail
