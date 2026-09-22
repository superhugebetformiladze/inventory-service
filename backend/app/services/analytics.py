"""Подсчёт показателей по загруженным данным.

Здесь собираются все числа, которые видит пользователь. Основное понятие —
«учтённое время»: сумма всех интервалов, попавших в выборку. От него считаются доли.

  * коэффициент загрузки = время работы по программе ÷ учтённое время;
  * загрузка при включённом станке = то же, но из знаменателя убрано время,
    когда оборудование было выключено или не выходило на связь.

Состояния различаются по коду из выгрузки: 0101 — работа по программе,
0001 и 0002 — выключено и нет данных. Всё остальное считается простоем.
"""

from __future__ import annotations

from dataclasses import dataclass, replace
from datetime import datetime

from sqlalchemy import Select, func, select, tuple_
from sqlalchemy.orm import Session

from ..models import MonitoringRecord, SourceFile
from . import equipment

# Коды состояний (MachineParam.IntegrationId) в системе мониторинга
CODE_WORK = "0101"          # работа по программе
CODES_OFF = ("0001", "0002")  # оборудование выключено / нет данных


@dataclass
class Query:
    """Что именно показываем: один файл или диапазон дат по всем файлам."""

    scope: str = "file"  # file | range
    file_id: int | None = None
    date_from: datetime | None = None
    date_to: datetime | None = None
    schedule_type: int = 1
    shift: int | None = None
    machines: list[str] | None = None


def resolve_day_sources(db: Session, q: Query) -> list[tuple[int, datetime]]:
    """Для режима «по датам»: на каждые сутки берём данные самой свежей выгрузки.

    Выгрузки за соседние периоды перекрываются: одни и те же сутки могут быть
    и в августовском, и в сентябрьском файле. Если сложить всё подряд, эти сутки
    посчитаются дважды и показатели поедут. Поэтому для каждых суток берём данные
    только из одной выгрузки — самой свежей из тех, где эти сутки есть.
    """
    stmt = (
        select(
            MonitoringRecord.calc_date,
            MonitoringRecord.file_id,
            SourceFile.uploaded_at,
        )
        .join(SourceFile, SourceFile.id == MonitoringRecord.file_id)
        .where(MonitoringRecord.schedule_type_id == q.schedule_type)
        .group_by(MonitoringRecord.calc_date, MonitoringRecord.file_id, SourceFile.uploaded_at)
    )
    if q.date_from:
        stmt = stmt.where(MonitoringRecord.calc_date >= q.date_from)
    if q.date_to:
        stmt = stmt.where(MonitoringRecord.calc_date <= q.date_to)

    best: dict[datetime, tuple[datetime, int]] = {}
    for calc_date, file_id, uploaded_at in db.execute(stmt):
        current = best.get(calc_date)
        if current is None or (uploaded_at, file_id) > current:
            best[calc_date] = (uploaded_at, file_id)
    return [(file_id, calc_date) for calc_date, (_, file_id) in best.items()]


def base_query(db: Session, q: Query) -> Select:
    """Отбор строк, из которых потом считаются все показатели.

    Собирает условия по фильтрам пользователя: конкретная выгрузка или диапазон дат,
    режим учёта времени, смена, список станков. Дальше к этому отбору просто
    применяются суммирование и группировки.
    """
    stmt = select(MonitoringRecord).where(
        MonitoringRecord.schedule_type_id == q.schedule_type
    )

    if q.scope == "file":
        stmt = stmt.where(MonitoringRecord.file_id == q.file_id)
        if q.date_from:
            stmt = stmt.where(MonitoringRecord.calc_date >= q.date_from)
        if q.date_to:
            stmt = stmt.where(MonitoringRecord.calc_date <= q.date_to)
    else:
        pairs = resolve_day_sources(db, q)
        if not pairs:
            return stmt.where(MonitoringRecord.id < 0)
        stmt = stmt.where(
            tuple_(MonitoringRecord.file_id, MonitoringRecord.calc_date).in_(pairs)
        )

    if q.shift:
        stmt = stmt.where(MonitoringRecord.smena_num == q.shift)
    if q.machines:
        stmt = stmt.where(MonitoringRecord.machine_key.in_(q.machines))
    return stmt


def _filtered(db: Session, q: Query):
    """Подзапрос-CTE с отобранными записями, к которому цепляем агрегаты."""
    return base_query(db, q).subquery()


def aliases(db: Session) -> dict[str, str]:
    """machine_key -> название из карточки оборудования, если она заведена."""
    return equipment.titles(db)


def machine_title(key: str, alias_map: dict[str, str]) -> str:
    return alias_map.get(key) or key


def _share(part: float, whole: float) -> float:
    """Доля в процентах. Если знаменатель нулевой (данных нет) — возвращаем 0,
    а не падаем с делением на ноль."""
    return round(part / whole * 100, 2) if whole else 0.0


def overview(db: Session, q: Query) -> dict:
    sub = _filtered(db, q)

    totals_row = db.execute(
        select(
            func.coalesce(func.sum(sub.c.seconds), 0.0),
            func.count(sub.c.id),
            func.count(func.distinct(sub.c.machine_key)),
            func.count(func.distinct(sub.c.calc_date)),
            func.min(sub.c.calc_date),
            func.max(sub.c.calc_date),
            func.coalesce(func.sum(sub.c.count_state), 0),
        )
    ).one()

    total_seconds, records_count, machines_count, days_count, date_min, date_max, events = totals_row

    by_code = {
        code: float(seconds or 0)
        for code, seconds in db.execute(
            select(sub.c.param_code, func.sum(sub.c.seconds)).group_by(sub.c.param_code)
        )
    }
    work_seconds = by_code.get(CODE_WORK, 0.0)
    off_seconds = sum(by_code.get(code, 0.0) for code in CODES_OFF)
    total_seconds = float(total_seconds or 0)
    powered_seconds = max(total_seconds - off_seconds, 0.0)
    downtime_seconds = max(total_seconds - work_seconds - off_seconds, 0.0)

    groups = [
        {
            "group": name or "Без группы",
            "ext_id": ext_id,
            "seconds": float(seconds or 0),
            "share": _share(float(seconds or 0), total_seconds),
        }
        for name, ext_id, seconds in db.execute(
            select(sub.c.group_name, sub.c.group_ext_id, func.sum(sub.c.seconds))
            .group_by(sub.c.group_name, sub.c.group_ext_id)
            .order_by(func.sum(sub.c.seconds).desc())
        )
    ]

    params = [
        {
            "name": name,
            "code": code,
            "color": color,
            "group": group or "Без группы",
            "seconds": float(seconds or 0),
            "share": _share(float(seconds or 0), total_seconds),
            "events": int(events_count or 0),
        }
        for name, code, color, group, seconds, events_count in db.execute(
            select(
                sub.c.param_name,
                sub.c.param_code,
                sub.c.param_color,
                sub.c.group_name,
                func.sum(sub.c.seconds),
                func.sum(sub.c.count_state),
            )
            .group_by(sub.c.param_name, sub.c.param_code, sub.c.param_color, sub.c.group_name)
            .order_by(func.sum(sub.c.seconds).desc())
        )
    ]

    return {
        "scope": q.scope,
        "schedule_type": q.schedule_type,
        "shift": q.shift,
        "totals": {
            "total_seconds": total_seconds,
            "work_seconds": work_seconds,
            "off_seconds": off_seconds,
            "powered_seconds": powered_seconds,
            "downtime_seconds": downtime_seconds,
            "utilization": _share(work_seconds, total_seconds),
            "utilization_powered": _share(work_seconds, powered_seconds),
            "records": int(records_count or 0),
            "events": int(events or 0),
            "machines": int(machines_count or 0),
            "days": int(days_count or 0),
            "date_min": date_min,
            "date_max": date_max,
        },
        "by_group": groups,
        "by_param": params,
    }


def by_machine(db: Session, q: Query) -> list[dict]:
    sub = _filtered(db, q)
    alias_map = aliases(db)

    rows = db.execute(
        select(
            sub.c.machine_key,
            sub.c.param_code,
            sub.c.group_name,
            func.sum(sub.c.seconds),
        ).group_by(sub.c.machine_key, sub.c.param_code, sub.c.group_name)
    ).all()

    acc: dict[str, dict] = {}
    for machine_key, code, group, seconds in rows:
        seconds = float(seconds or 0)
        item = acc.setdefault(
            machine_key,
            {
                "key": machine_key,
                "name": machine_title(machine_key, alias_map),
                "total_seconds": 0.0,
                "work_seconds": 0.0,
                "off_seconds": 0.0,
                "groups": {},
            },
        )
        item["total_seconds"] += seconds
        if code == CODE_WORK:
            item["work_seconds"] += seconds
        if code in CODES_OFF:
            item["off_seconds"] += seconds
        group_name = group or "Без группы"
        item["groups"][group_name] = item["groups"].get(group_name, 0.0) + seconds

    result = []
    for item in acc.values():
        total = item["total_seconds"]
        powered = max(total - item["off_seconds"], 0.0)
        item["downtime_seconds"] = max(total - item["work_seconds"] - item["off_seconds"], 0.0)
        item["utilization"] = _share(item["work_seconds"], total)
        item["utilization_powered"] = _share(item["work_seconds"], powered)
        result.append(item)

    result.sort(key=lambda row: row["utilization"], reverse=True)
    return result


def by_day(db: Session, q: Query) -> list[dict]:
    sub = _filtered(db, q)
    rows = db.execute(
        select(sub.c.calc_date, sub.c.param_code, func.sum(sub.c.seconds))
        .group_by(sub.c.calc_date, sub.c.param_code)
        .order_by(sub.c.calc_date)
    ).all()

    acc: dict[datetime, dict] = {}
    for calc_date, code, seconds in rows:
        seconds = float(seconds or 0)
        item = acc.setdefault(
            calc_date,
            {
                "date": calc_date,
                "total_seconds": 0.0,
                "work_seconds": 0.0,
                "off_seconds": 0.0,
            },
        )
        item["total_seconds"] += seconds
        if code == CODE_WORK:
            item["work_seconds"] += seconds
        if code in CODES_OFF:
            item["off_seconds"] += seconds

    days = []
    for item in sorted(acc.values(), key=lambda row: row["date"]):
        total = item["total_seconds"]
        item["downtime_seconds"] = max(total - item["work_seconds"] - item["off_seconds"], 0.0)
        item["utilization"] = _share(item["work_seconds"], total)
        days.append(item)
    return days


def heatmap(db: Session, q: Query) -> dict:
    """Матрица «станок × сутки» с коэффициентом загрузки."""
    sub = _filtered(db, q)
    alias_map = aliases(db)
    rows = db.execute(
        select(
            sub.c.machine_key,
            sub.c.calc_date,
            sub.c.param_code,
            func.sum(sub.c.seconds),
        ).group_by(sub.c.machine_key, sub.c.calc_date, sub.c.param_code)
    ).all()

    cells: dict[tuple[str, datetime], dict] = {}
    dates: set[datetime] = set()
    for machine_key, calc_date, code, seconds in rows:
        seconds = float(seconds or 0)
        dates.add(calc_date)
        cell = cells.setdefault((machine_key, calc_date), {"total": 0.0, "work": 0.0})
        cell["total"] += seconds
        if code == CODE_WORK:
            cell["work"] += seconds

    machines = sorted({key for key, _ in cells})
    return {
        "dates": [d.date().isoformat() for d in sorted(dates)],
        "machines": [
            {"key": key, "name": machine_title(key, alias_map)} for key in machines
        ],
        "cells": [
            {
                "machine": key,
                "date": calc_date.date().isoformat(),
                "utilization": _share(cell["work"], cell["total"]),
                "work_seconds": cell["work"],
                "total_seconds": cell["total"],
            }
            for (key, calc_date), cell in cells.items()
        ],
    }


def records(db: Session, q: Query, limit: int = 100, offset: int = 0) -> dict:
    stmt = base_query(db, q)
    alias_map = aliases(db)

    total = db.scalar(select(func.count()).select_from(stmt.subquery())) or 0
    rows = db.scalars(
        stmt.order_by(
            MonitoringRecord.calc_date,
            MonitoringRecord.machine_key,
            MonitoringRecord.seconds.desc(),
        )
        .limit(limit)
        .offset(offset)
    ).all()

    return {
        "total": int(total),
        "limit": limit,
        "offset": offset,
        "items": [
            {
                "id": row.id,
                "machine": machine_title(row.machine_key, alias_map),
                "machine_key": row.machine_key,
                "param": row.param_name,
                "code": row.param_code,
                "color": row.param_color,
                "group": row.group_name,
                "calc_date": row.calc_date,
                "from_date": row.from_date,
                "to_date": row.to_date,
                "shift": row.smena_num,
                "seconds": row.seconds,
                "events": row.count_state,
            }
            for row in rows
        ],
    }


def machine_detail(db: Session, q: Query, machine_key: str) -> dict:
    """Всё про один станок: показатели, состояния, динамика и место в парке."""
    fleet = by_machine(db, q)
    # Коэффициент по парку считаем так же, как на дашборде: суммарная работа делится
    # на суммарное учтённое время. Среднее арифметическое по станкам дало бы другое
    # число (у станков разное количество учтённых часов), и два похожих процента
    # в интерфейсе означали бы разное.
    fleet_totals = overview(db, replace(q, machines=None))["totals"]
    position = next(
        (index + 1 for index, row in enumerate(fleet) if row["key"] == machine_key), None
    )

    single = replace(q, machines=[machine_key])
    summary = overview(db, single)
    days = by_day(db, single)
    rows = next((row for row in fleet if row["key"] == machine_key), None)

    worked = [day for day in days if day["work_seconds"] > 0]
    best = max(worked, key=lambda day: day["utilization"], default=None)
    worst = min(worked, key=lambda day: day["utilization"], default=None)

    return {
        "machine_key": machine_key,
        "totals": summary["totals"],
        "by_group": summary["by_group"],
        "by_param": summary["by_param"],
        "by_day": days,
        "machine": rows,
        "fleet": {
            "utilization": fleet_totals["utilization"],
            "average_utilization": (
                round(sum(row["utilization"] for row in fleet) / len(fleet), 2) if fleet else 0.0
            ),
            "machines": len(fleet),
            "position": position,
            "best_day": best,
            "worst_day": worst,
            "idle_days": sum(1 for day in days if day["work_seconds"] == 0),
        },
    }
