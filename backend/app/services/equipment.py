"""Карточки оборудования: импорт из Excel и привязка к станкам мониторинга.

Поддерживаются два вида таблиц:

1. **Выгрузка станков из системы мониторинга** (`..._machines.xlsx`). На листе есть
   колонка `ID` — это идентификатор станка в мониторинге, тот самый суффикс в
   `MDM-C19-002147500&36`. Вместе с `Код_MDM` он даёт точную привязку, гадать не нужно.
   Технический лист той же книги (INV_NUM / MODEL / IP / MDM …) дополняет карточки
   адресами и моделями; заголовки на нём съезжают относительно данных, поэтому колонки
   проверяются по содержимому (см. `repair_columns`).

2. **Таблица для присвоения кодов MDM** (Инв.№ + ID MDM без идентификатора станка).
   Точной привязки в ней нет: код MDM общий для одинаковых моделей, поэтому карточки
   раскладываются по порядку и помечаются `guess` — их нужно проверить глазами.

Логины и пароли доступа к станкам, если они есть в файле, сознательно не читаются
и в базу не попадают.
"""

from __future__ import annotations

import io
import re
from dataclasses import dataclass, field
from typing import Any

from sqlalchemy import select
from sqlalchemy.orm import Session

from ..models import EquipmentCard, MonitoringRecord

# Заголовки колонок: русские из выгрузки, английские из технического листа, старый формат
COLUMN_SYNONYMS: dict[str, tuple[str, ...]] = {
    "monitoring_id": ("id", "ид", "id станка"),
    "inv_number": (
        "инвентарный_номер",
        "инвентарный номер",
        "inv_num",
        "inv num",
        "инв.№",
        "инв. №",
        "инв №",
        "инв.номер",
        "инв",
    ),
    "model": ("модель станка", "модель", "model"),
    "name": ("описание", "description", "наименование оборудования", "наименование"),
    "ip_address": ("ip_адрес", "ip адрес", "ip"),
    "cnc_name": ("имя_учпу", "имя учпу"),
    "cnc_type": ("тип_учпу", "тип учпу"),
    "cnc_system": ("система чпу", "чпу"),
    "workshop": ("цех", "shop_id", "участок цеха"),
    "mdm_code": ("код_mdm", "код mdm", "id mdm*", "id mdm", "mdm"),
    "import_note": ("примечание", "комментарий", "прим."),
    "row_no": ("№ п/п", "n п/п", "номер п/п"),
}

MDM_RE = re.compile(r"^MDM-[A-Za-z0-9-]+", re.IGNORECASE)
IPV4_RE = re.compile(r"^\d{1,3}(\.\d{1,3}){3}$")
# "0005559 (26 (80))" -> инвентарный номер и номер станка в цехе
INV_RE = re.compile(r"^\s*([^()]+?)\s*(?:\(\s*([^()]*?)\s*(?:\(.*\))?\s*\))?\s*$")

# Поля, которые импорт обновляет в карточке
IMPORTED_FIELDS = (
    "mdm_code",
    "monitoring_id",
    "workshop",
    "name",
    "model",
    "cnc_system",
    "cnc_name",
    "cnc_type",
    "ip_address",
    "shop_number",
    "import_note",
    "row_no",
)


@dataclass
class ImportReport:
    created: int = 0
    updated: int = 0
    skipped: list[str] = field(default_factory=list)
    bound_exact: int = 0
    bound_guess: int = 0
    cards_without_machine: list[str] = field(default_factory=list)
    machines_without_card: list[str] = field(default_factory=list)
    unknown_machines: list[str] = field(default_factory=list)
    sheets: list[str] = field(default_factory=list)

    def as_dict(self) -> dict[str, Any]:
        return {
            "sheets": self.sheets,
            "created": self.created,
            "updated": self.updated,
            "skipped": self.skipped,
            "bound_exact": self.bound_exact,
            "bound_guess": self.bound_guess,
            "cards_without_machine": self.cards_without_machine,
            "machines_without_card": self.machines_without_card,
            "unknown_machines": self.unknown_machines,
        }


class ExcelImportError(ValueError):
    pass


def _normalize(value: Any) -> str:
    text = "" if value is None else str(value)
    return re.sub(r"\s+", " ", text).strip().lower()


def _clean(value: Any) -> str | None:
    if value is None:
        return None
    text = re.sub(r"\s+", " ", str(value)).strip()
    return text or None


def inv_key(value: Any) -> str:
    """Ключ для сопоставления инвентарных номеров между таблицами.

    В разных выгрузках один и тот же станок записан как «C0005476», «5476» или
    «0004658 (7)», поэтому сравниваем только значащие цифры.
    """
    text = str(value or "").split("(")[0]
    digits = re.sub(r"\D", "", text).lstrip("0")
    return digits or re.sub(r"\W", "", text).upper()


def split_inv(value: Any) -> tuple[str | None, str | None]:
    """«0005559 (26 (80))» -> ('0005559', '26')."""
    text = _clean(value)
    if not text:
        return None, None
    match = INV_RE.match(text)
    if not match:
        return text, None
    number = (match.group(1) or "").strip() or None
    shop_number = (match.group(2) or "").strip() or None
    return number, shop_number


def _looks_like_mdm(value: Any) -> bool:
    return bool(MDM_RE.match(str(value or "").strip()))


def _looks_like_ip(value: Any) -> bool:
    return bool(IPV4_RE.match(str(value or "").strip()))


def _looks_like_id(value: Any) -> bool:
    try:
        number = int(str(value).strip())
    except (TypeError, ValueError):
        return False
    return 0 < number < 100000


VALIDATORS = {
    "mdm_code": _looks_like_mdm,
    "ip_address": _looks_like_ip,
    "monitoring_id": _looks_like_id,
}


def detect_columns(row: tuple[Any, ...]) -> dict[str, int]:
    found: dict[str, int] = {}
    for index, cell in enumerate(row):
        header = _normalize(cell)
        if not header:
            continue
        for field_name, variants in COLUMN_SYNONYMS.items():
            if field_name in found:
                continue
            if header in variants:
                found[field_name] = index
                break
    return found


def repair_columns(
    columns: dict[str, int], sample: list[tuple[Any, ...]]
) -> tuple[dict[str, int], int | None]:
    """Чинит съехавшие заголовки: колонку ищем по содержимому.

    На техническом листе выгрузки заголовки сдвинуты относительно данных — значение
    кода MDM лежит под заголовком `UPLOAD_VKO`. Поэтому для полей с узнаваемым
    форматом (код MDM, IP, ID) проверяем реальные значения и при необходимости
    переносим колонку.
    """
    if not sample:
        return columns, None

    fixed = dict(columns)
    shifted = False
    verified: list[int] = []
    for name, validator in VALIDATORS.items():
        index = fixed.get(name)
        if index is not None and _column_matches(sample, index, validator):
            verified.append(index)
            continue

        # Колонка есть, но пустая (например, IP в выгрузке не заполняют) — это не сдвиг
        if index is not None and _column_is_empty(sample, index):
            fixed.pop(name)
            continue

        taken = {position for key, position in fixed.items() if key != name}
        width = max(len(row) for row in sample)
        candidates = [
            position
            for position in range(width)
            if position not in taken and _column_matches(sample, position, validator)
        ]
        if len(candidates) == 1 or (candidates and name != "monitoring_id"):
            fixed[name] = candidates[0]
            if index is not None:
                shifted = True
        elif index is not None:
            fixed.pop(name)
            shifted = True

    if not shifted:
        return fixed, None

    # Заголовки не совпадают с данными. Доверяем только колонкам до последней, которая
    # подтвердилась на своём месте: дальше значения принадлежат соседним столбцам.
    safe_limit = max(verified) if verified else -1
    checked = set(VALIDATORS)
    trimmed = {
        name: position
        for name, position in fixed.items()
        if name in checked or position <= safe_limit
    }
    return trimmed, safe_limit


def _column_is_empty(sample: list[tuple[Any, ...]], index: int) -> bool:
    return not any(
        row[index] not in (None, "") for row in sample if index < len(row)
    )


def _column_matches(sample: list[tuple[Any, ...]], index: int, validator) -> bool:
    values = [row[index] for row in sample if index < len(row) and row[index] not in (None, "")]
    if not values:
        return False
    hits = sum(1 for value in values if validator(value))
    return hits >= max(1, int(len(values) * 0.8))


def read_sheet(sheet) -> tuple[dict[str, int], list[tuple[Any, ...]], int | None] | None:
    """Ищем строку заголовков и возвращаем сопоставление колонок и строки данных."""
    head = list(sheet.iter_rows(max_row=min(sheet.max_row or 1, 30), values_only=True))
    for index, row in enumerate(head):
        columns = detect_columns(row)
        if "inv_number" not in columns and "mdm_code" not in columns:
            continue
        data = [
            item
            for item in sheet.iter_rows(min_row=index + 2, values_only=True)
            if any(cell not in (None, "") for cell in item)
        ]
        if not data:
            continue
        repaired, safe_limit = repair_columns(columns, data[:20])
        return repaired, data, safe_limit
    return None


def parse_row(columns: dict[str, int], row: tuple[Any, ...]) -> dict[str, Any]:
    def cell(name: str) -> Any:
        position = columns.get(name)
        if position is None or position >= len(row):
            return None
        return row[position]

    inv_number, shop_number = split_inv(cell("inv_number"))
    mdm_code = _clean(cell("mdm_code"))
    monitoring_id = cell("monitoring_id")
    row_no = cell("row_no")

    return {
        "inv_number": inv_number,
        "shop_number": shop_number,
        "mdm_code": mdm_code if mdm_code and _looks_like_mdm(mdm_code) else None,
        "monitoring_id": int(monitoring_id) if _looks_like_id(monitoring_id) else None,
        "model": _clean(cell("model")),
        "name": _clean(cell("name")),
        "ip_address": _clean(cell("ip_address")) if _looks_like_ip(cell("ip_address")) else None,
        "cnc_name": _clean(cell("cnc_name")),
        "cnc_type": _clean(cell("cnc_type")),
        "cnc_system": _clean(cell("cnc_system")),
        "workshop": _clean(cell("workshop")),
        "import_note": _clean(cell("import_note")),
        "row_no": int(row_no) if _looks_like_id(row_no) else None,
    }


def machine_suffix(machine_key: str) -> int:
    match = re.search(r"&(\d+)$", machine_key or "")
    return int(match.group(1)) if match else 0


def known_machine_keys(db: Session) -> list[str]:
    return sorted(
        db.scalars(select(MonitoringRecord.machine_key).distinct()).all(),
        key=machine_suffix,
    )


def rebind(db: Session, report: ImportReport | None = None) -> ImportReport:
    """Расставляет привязки карточек к станкам мониторинга.

    Точную привязку даёт `monitoring_id` из выгрузки станков. Остальные карточки
    раскладываются по коду MDM в порядке возрастания суффикса — это предположение.
    Привязки, выставленные руками, не трогаем.
    """
    report = report or ImportReport()
    cards = list(db.scalars(select(EquipmentCard)))
    machines = known_machine_keys(db)
    known = set(machines)

    manual = {card.machine_key for card in cards if card.binding_kind == "manual" and card.machine_key}

    for card in cards:
        if card.binding_kind != "manual":
            card.machine_key = None
            card.binding_kind = "none"
    # Сначала снимаем все привязки в БД, иначе перестановка станков между карточками
    # упрётся в уникальный индекс.
    db.flush()

    taken = set(manual)
    rest: list[EquipmentCard] = []

    for card in sorted(cards, key=lambda item: item.monitoring_id or 0):
        if card.binding_kind == "manual":
            continue
        if card.monitoring_id and card.mdm_code:
            key = "{0}&{1}".format(card.mdm_code, card.monitoring_id)
            if key in taken:
                report.skipped.append(
                    "станок {0} указан у нескольких карточек".format(key)
                )
                rest.append(card)
                continue
            card.machine_key = key
            card.binding_kind = "auto"
            taken.add(key)
            report.bound_exact += 1
            if key not in known:
                report.unknown_machines.append("{0} ({1})".format(key, card.inv_number))
        else:
            rest.append(card)

    db.flush()

    # Старый формат без ID — раскладываем по порядку внутри кода MDM
    by_code: dict[str, list[EquipmentCard]] = {}
    for card in rest:
        if card.mdm_code:
            by_code.setdefault(card.mdm_code, []).append(card)

    free_by_code: dict[str, list[str]] = {}
    for key in machines:
        if key in taken:
            continue
        free_by_code.setdefault(key.split("&")[0], []).append(key)

    for code, group in by_code.items():
        available = free_by_code.get(code, [])
        group.sort(key=lambda item: (item.row_no or 0, item.inv_number or ""))
        for card, key in zip(group, available):
            card.machine_key = key
            card.binding_kind = "guess"
            taken.add(key)
            report.bound_guess += 1

    db.flush()

    bound = {card.machine_key for card in cards if card.machine_key}
    report.cards_without_machine = sorted(card.inv_number for card in cards if not card.machine_key)
    report.machines_without_card = sorted(
        (key for key in machines if key not in bound), key=machine_suffix
    )
    return report


def import_excel(db: Session, content: bytes) -> ImportReport:
    from openpyxl import load_workbook

    try:
        workbook = load_workbook(io.BytesIO(content), data_only=True, read_only=True)
    except Exception as exc:  # noqa: BLE001 - показываем пользователю понятную причину
        raise ExcelImportError("Не удалось прочитать файл Excel: {0}".format(exc)) from exc

    report = ImportReport()
    cards_by_key: dict[str, EquipmentCard] = {
        inv_key(card.inv_number): card for card in db.scalars(select(EquipmentCard))
    }
    seen_sheets = 0

    for sheet in workbook.worksheets:
        parsed = read_sheet(sheet)
        if not parsed:
            continue
        columns, rows, safe_limit = parsed
        seen_sheets += 1
        report.sheets.append(
            sheet.title
            if safe_limit is None
            else "{0} (заголовки съехали — взяты только проверяемые колонки)".format(sheet.title)
        )

        for position, raw in enumerate(rows, start=1):
            item = parse_row(columns, raw)
            if not item["inv_number"]:
                report.skipped.append(
                    "{0}, строка {1}: нет инвентарного номера".format(sheet.title, position)
                )
                continue

            key = inv_key(item["inv_number"])
            card = cards_by_key.get(key)
            if card is None:
                card = EquipmentCard(inv_number=item["inv_number"])
                db.add(card)
                cards_by_key[key] = card
                report.created += 1
            else:
                report.updated += 1
                # В разных выгрузках номер записан как «2868» или «00002868» —
                # оставляем более полную запись, карточка та же самая.
                if len(item["inv_number"]) > len(card.inv_number or ""):
                    card.inv_number = item["inv_number"]

            # Второй лист той же книги дополняет карточку, но не затирает уже известное
            for name in IMPORTED_FIELDS:
                value = item.get(name)
                if value not in (None, ""):
                    setattr(card, name, value)

    if not seen_sheets:
        raise ExcelImportError(
            "В файле не нашлась таблица оборудования: нужна колонка с инвентарным "
            "номером и код MDM (или колонка ID из выгрузки станков)"
        )

    db.flush()
    rebind(db, report)
    db.commit()
    return report


def titles(db: Session) -> dict[str, str]:
    """machine_key -> человеческое название станка."""
    return {
        card.machine_key: card.title
        for card in db.scalars(select(EquipmentCard).where(EquipmentCard.machine_key.is_not(None)))
    }
