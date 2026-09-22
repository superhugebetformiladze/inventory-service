"""Разбор XML-выгрузки системы мониторинга станков.

Система мониторинга отдаёт один большой XML-файл: справочники (станки, состояния,
графики смен) и главные данные — сколько времени каждый станок провёл в каждом
состоянии за смену или сутки. Задача этого модуля — превратить файл в простые
списки словарей, которые дальше просто складываются в таблицы базы.

Две особенности формата, которые стоит знать:
  * длительность записана как PT6H52M37S — это 6 часов 52 минуты 37 секунд;
  * цвет состояния приходит числом вроде -16711936 (формат .NET) и переводится
    в привычный вид #00ff00.


Структура файла:
    <XmlDataWrapper>
      <DateLastReplication>      — метаданные выгрузки (период, время формирования)
      <Machine>                  — станки (ID + IntegrationID)
      <MachineParam>             — состояния (работа по программе, простой, авария, ...)
      <GroupParam>               — группы состояний (Тмаш, Тпрос.орг и т.д.)
      <MachineParamInGroup>      — связь состояние -> группа
      <rep_MonitoringByPriority> — интервалы: станок/состояние/смена/сутки + длительность
      <SheduleMeta/SheduleBase/SheduleBaseItem> — графики смен
    </XmlDataWrapper>
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Iterable

DURATION_RE = re.compile(
    r"^P(?:(?P<days>\d+)D)?"
    r"(?:T(?:(?P<hours>\d+)H)?(?:(?P<minutes>\d+)M)?(?:(?P<seconds>[\d.]+)S)?)?$"
)

# SheduleTypeID в rep_MonitoringByPriority
SCHEDULE_TYPE_SHIFT = 1  # учёт по графику смен
SCHEDULE_TYPE_DAY = 2    # учёт по календарным суткам (24 ч)


class XmlParseError(ValueError):
    pass


def parse_duration(value: str | None) -> float:
    """'PT6H52M37S' -> 24757.0 секунд."""
    if not value:
        return 0.0
    match = DURATION_RE.match(value.strip())
    if not match:
        return 0.0
    parts = match.groupdict()
    return (
        int(parts["days"] or 0) * 86400
        + int(parts["hours"] or 0) * 3600
        + int(parts["minutes"] or 0) * 60
        + float(parts["seconds"] or 0)
    )


def parse_dt(value: str | None) -> datetime | None:
    if not value:
        return None
    text = value.strip()
    if not text:
        return None
    try:
        dt = datetime.fromisoformat(text)
    except ValueError:
        for fmt in ("%Y-%m-%dT%H:%M:%S.%f", "%Y-%m-%dT%H:%M:%S", "%Y-%m-%d"):
            try:
                dt = datetime.strptime(text, fmt)
                break
            except ValueError:
                continue
        else:
            return None
    return dt.replace(tzinfo=None)


def parse_int(value: str | None) -> int | None:
    if value is None or not value.strip():
        return None
    try:
        return int(float(value.strip()))
    except ValueError:
        return None


def argb_to_hex(value: str | None) -> str | None:
    """.NET-цвет (знаковый ARGB int, напр. -16711936) -> '#00ff00'."""
    number = parse_int(value)
    if number is None:
        return None
    return "#{:06x}".format(number & 0xFFFFFF)


@dataclass
class ParsedFile:
    replication_name: str | None = None
    replication_dt: datetime | None = None
    monitoring_from: datetime | None = None
    monitoring_to: datetime | None = None

    machines: list[dict[str, Any]] = field(default_factory=list)
    groups: list[dict[str, Any]] = field(default_factory=list)
    params: list[dict[str, Any]] = field(default_factory=list)
    records: list[dict[str, Any]] = field(default_factory=list)
    shifts: list[dict[str, Any]] = field(default_factory=list)

    @property
    def date_min(self) -> datetime | None:
        dates = [r["calc_date"] for r in self.records if r["calc_date"]]
        return min(dates) if dates else None

    @property
    def date_max(self) -> datetime | None:
        dates = [r["calc_date"] for r in self.records if r["calc_date"]]
        return max(dates) if dates else None


def _rows(root, section: str) -> Iterable[dict[str, str | None]]:
    """Секции однородны: <Section><Section>...поля...</Section></Section>."""
    node = root.find(section)
    if node is None:
        return []
    return ({child.tag: child.text for child in item} for item in node)


def parse_xml(source: bytes | str) -> ParsedFile:
    from lxml import etree

    # resolve_entities=False + no_network=True закрывают XXE и обращения наружу,
    # huge_tree не включаем — иначе снимаются встроенные лимиты libxml2 на размер дерева.
    parser = etree.XMLParser(
        recover=True,
        resolve_entities=False,
        no_network=True,
        load_dtd=False,
        dtd_validation=False,
    )
    try:
        if isinstance(source, bytes):
            root = etree.fromstring(source, parser=parser)
        else:
            root = etree.parse(source, parser=parser).getroot()
    except etree.XMLSyntaxError as exc:
        raise XmlParseError("Некорректный XML: {0}".format(exc)) from exc

    if root is None or root.tag != "XmlDataWrapper":
        got = getattr(root, "tag", "?")
        raise XmlParseError(
            "Ожидался корневой элемент <XmlDataWrapper>, получен <{0}>".format(got)
        )

    parsed = ParsedFile()

    for row in _rows(root, "DateLastReplication"):
        parsed.replication_name = (row.get("Description") or row.get("Name") or "").strip() or None
        parsed.replication_dt = parse_dt(row.get("Dt"))
        parsed.monitoring_from = parse_dt(row.get("MonitoringFrom"))
        parsed.monitoring_to = parse_dt(row.get("MonitoringTo"))
        break

    machine_keys: dict[int, str] = {}
    for row in _rows(root, "Machine"):
        ext_id = parse_int(row.get("ID"))
        if ext_id is None:
            continue
        integration_id = (row.get("IntegrationID") or "machine-{0}".format(ext_id)).strip()
        machine_keys[ext_id] = integration_id
        parsed.machines.append({"ext_id": ext_id, "integration_id": integration_id})

    groups: dict[int, str] = {}
    for row in _rows(root, "GroupParam"):
        ext_id = parse_int(row.get("ID"))
        if ext_id is None:
            continue
        name = (row.get("Name") or "").strip()
        groups[ext_id] = name
        parsed.groups.append({"ext_id": ext_id, "name": name})

    param_to_group: dict[int, int] = {}
    for row in _rows(root, "MachineParamInGroup"):
        param_id = parse_int(row.get("MachineParamID"))
        group_id = parse_int(row.get("GroupParamID"))
        if param_id is not None and group_id is not None:
            param_to_group[param_id] = group_id

    params: dict[int, dict[str, Any]] = {}
    for row in _rows(root, "MachineParam"):
        ext_id = parse_int(row.get("ID"))
        if ext_id is None:
            continue
        group_ext_id = param_to_group.get(ext_id)
        item = {
            "ext_id": ext_id,
            "name": (row.get("Name") or "").strip(),
            "type_id": parse_int(row.get("MachineParamTypeID")),
            "integration_id": (row.get("IntegrationId") or "").strip() or None,
            "color": argb_to_hex(row.get("DefaultColor")),
            "group_ext_id": group_ext_id,
            "group_name": groups.get(group_ext_id) if group_ext_id else None,
        }
        params[ext_id] = item
        parsed.params.append(item)

    for row in _rows(root, "rep_MonitoringByPriority"):
        calc_date = parse_dt(row.get("CalcDate"))
        machine_ext_id = parse_int(row.get("MachineID"))
        param_ext_id = parse_int(row.get("MachineParamID"))
        if calc_date is None or machine_ext_id is None or param_ext_id is None:
            continue
        param = params.get(param_ext_id, {})
        parsed.records.append(
            {
                "ext_id": parse_int(row.get("ID")) or 0,
                "machine_ext_id": machine_ext_id,
                "machine_key": machine_keys.get(machine_ext_id, "machine-{0}".format(machine_ext_id)),
                "param_ext_id": param_ext_id,
                "param_name": param.get("name") or "Параметр {0}".format(param_ext_id),
                "param_code": param.get("integration_id"),
                "param_color": param.get("color"),
                "group_ext_id": param.get("group_ext_id"),
                "group_name": param.get("group_name") or "Без группы",
                "from_date": parse_dt(row.get("FromDate")),
                "to_date": parse_dt(row.get("ToDate")),
                "calc_date": calc_date,
                "seconds": parse_duration(row.get("StateTime")),
                "smena_num": parse_int(row.get("SmenaNum")),
                "count_state": parse_int(row.get("CountState")) or 0,
                "schedule_type_id": parse_int(row.get("SheduleTypeID")) or SCHEDULE_TYPE_SHIFT,
            }
        )

    meta: dict[int, dict[str, Any]] = {}
    for row in _rows(root, "SheduleMeta"):
        ext_id = parse_int(row.get("ID"))
        if ext_id is None:
            continue
        meta[ext_id] = {
            "is_holiday": bool(parse_int(row.get("IsHoliday"))),
            "shedule_date": parse_dt(row.get("SheduleDate")),
        }

    bases: dict[int, dict[str, Any]] = {}
    for row in _rows(root, "SheduleBase"):
        ext_id = parse_int(row.get("ID"))
        if ext_id is None:
            continue
        machine_ext_id = parse_int(row.get("MachineID")) or -1
        meta_row = meta.get(parse_int(row.get("SheduleMetaID")) or -1, {})
        bases[ext_id] = {
            "machine_ext_id": machine_ext_id,
            "machine_key": machine_keys.get(machine_ext_id, "machine-{0}".format(machine_ext_id)),
            "fix_date": parse_dt(row.get("FixDate")) or meta_row.get("shedule_date"),
            "smena_num": parse_int(row.get("SmenaNum")),
            "name": (row.get("Name") or "").strip() or None,
            "is_holiday": bool(meta_row.get("is_holiday")),
        }

    for row in _rows(root, "SheduleBaseItem"):
        base = bases.get(parse_int(row.get("SheduleBaseID")) or -1)
        if not base:
            continue
        shift = dict(base)
        shift["time_from"] = parse_duration(row.get("TimeFrom"))
        shift["time_to"] = parse_duration(row.get("TimeTo"))
        parsed.shifts.append(shift)

    if not parsed.records:
        raise XmlParseError("В файле не найдено ни одной записи rep_MonitoringByPriority")

    return parsed
