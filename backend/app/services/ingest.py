"""Приём загруженного файла: сохранить, разобрать, положить в базу.

Порядок такой: файл пишется на диск под случайным именем, затем разбирается
парсером, и полученные строки пачками вставляются в таблицы. Если файл загружают
повторно (например, поверх исправленной версии), старые строки этого файла сначала
удаляются — так данные не задваиваются.
"""

from __future__ import annotations

import hashlib
import uuid
from pathlib import Path

from sqlalchemy import delete, select
from sqlalchemy.orm import Session

from ..config import settings
from ..models import FileGroup, FileMachine, FileParam, MonitoringRecord, Shift, SourceFile
from ..parser import ParsedFile, XmlParseError, parse_xml

CHUNK = 2000


def clean_name(original_name: str) -> str:
    """Имя файла приходит от клиента и потом показывается в списке и в заголовке
    Content-Disposition. Оставляем только последний сегмент пути без управляющих
    символов — на диск оно всё равно не попадает, но и вводить в заблуждение не должно."""
    name = original_name.replace("\\", "/").split("/")[-1]
    name = "".join(char for char in name if char.isprintable() and char not in '"')
    name = name.strip().lstrip(".") or "upload.xml"
    return name[:255]


def save_upload(content: bytes, original_name: str) -> tuple[Path, str]:
    digest = hashlib.sha256(content).hexdigest()
    # Имя на диске всегда генерируем сами — пользовательское в путь не попадает.
    stored_name = "{0}.xml".format(uuid.uuid4().hex)
    path = settings.storage_dir / stored_name
    path.write_bytes(content)
    return path, digest


def find_duplicate(db: Session, digest: str) -> SourceFile | None:
    return db.scalar(select(SourceFile).where(SourceFile.sha256 == digest))


def clear_file_data(db: Session, file_id: int) -> None:
    for model in (MonitoringRecord, Shift, FileParam, FileGroup, FileMachine):
        db.execute(delete(model).where(model.file_id == file_id))


def load_parsed(db: Session, source: SourceFile, parsed: ParsedFile) -> None:
    clear_file_data(db, source.id)

    db.bulk_insert_mappings(
        FileMachine,
        [dict(file_id=source.id, **item) for item in parsed.machines],
    )
    db.bulk_insert_mappings(
        FileGroup,
        [dict(file_id=source.id, **item) for item in parsed.groups],
    )
    db.bulk_insert_mappings(
        FileParam,
        [dict(file_id=source.id, **item) for item in parsed.params],
    )
    db.bulk_insert_mappings(
        Shift,
        [dict(file_id=source.id, **item) for item in parsed.shifts],
    )

    records = [dict(file_id=source.id, **item) for item in parsed.records]
    for start in range(0, len(records), CHUNK):
        db.bulk_insert_mappings(MonitoringRecord, records[start : start + CHUNK])

    source.replication_name = parsed.replication_name
    source.replication_dt = parsed.replication_dt
    source.monitoring_from = parsed.monitoring_from
    source.monitoring_to = parsed.monitoring_to
    source.machines_count = len(parsed.machines)
    source.params_count = len(parsed.params)
    source.records_count = len(parsed.records)
    source.date_min = parsed.date_min
    source.date_max = parsed.date_max
    source.status = "ok"
    source.error = None


def ingest_bytes(db: Session, source: SourceFile, content: bytes) -> SourceFile:
    try:
        parsed = parse_xml(content)
    except XmlParseError as exc:
        source.status = "error"
        source.error = str(exc)
        db.commit()
        raise

    load_parsed(db, source, parsed)
    db.commit()
    db.refresh(source)
    return source


def remove_file(db: Session, source: SourceFile) -> None:
    path = settings.storage_dir / source.stored_name
    clear_file_data(db, source.id)
    db.delete(source)
    db.commit()
    path.unlink(missing_ok=True)
