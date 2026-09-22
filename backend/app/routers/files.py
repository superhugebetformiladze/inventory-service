"""Загрузка и хранение XML-выгрузок.

Что происходит при загрузке файла:
  1. проверяем расширение и размер (читаем по кускам, чтобы огромный файл не съел память);
  2. считаем от содержимого контрольную сумму и по ней узнаём, не грузили ли это раньше;
  3. кладём файл на диск под случайным именем (имя от пользователя в путь не попадает);
  4. разбираем XML и раскладываем данные по таблицам.

Исходный файл сохраняется целиком: его можно скачать или перечитать заново, если
в разборе что-то поправили.
"""

from fastapi import APIRouter, Depends, File, HTTPException, UploadFile, status
from fastapi.responses import FileResponse
from sqlalchemy import select
from sqlalchemy.orm import Session

from ..config import settings
from ..database import get_db
from ..deps import get_current_user, require_admin
from ..models import SourceFile, User
from ..parser import XmlParseError
from ..schemas import FileOut
from ..services import ingest

router = APIRouter(prefix="/api/files", tags=["files"])


def to_out(source: SourceFile) -> FileOut:
    payload = {column.name: getattr(source, column.name) for column in SourceFile.__table__.columns}
    payload["uploaded_by"] = source.uploaded_by.username if source.uploaded_by else None
    return FileOut.model_validate(payload)


@router.get("", response_model=list[FileOut])
def list_files(
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[FileOut]:
    rows = db.scalars(select(SourceFile).order_by(SourceFile.uploaded_at.desc()))
    return [to_out(row) for row in rows]


@router.get("/{file_id}", response_model=FileOut)
def get_file(
    file_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> FileOut:
    source = db.get(SourceFile, file_id)
    if source is None:
        raise HTTPException(status_code=404, detail="Файл не найден")
    return to_out(source)


@router.post("/upload", response_model=FileOut, status_code=status.HTTP_201_CREATED)
def upload_file(
    file: UploadFile = File(...),
    replace: bool = False,
    db: Session = Depends(get_db),
    admin: User = Depends(require_admin),
) -> FileOut:
    name = ingest.clean_name(file.filename or "upload.xml")
    if not name.lower().endswith(".xml"):
        raise HTTPException(status_code=400, detail="Ожидается файл .xml")

    # Читаем по кускам и обрываем на лимите: иначе гигантская загрузка съест память
    # ещё до проверки размера (nginx-лимит не спасает при прямом обращении к API).
    limit = settings.max_upload_mb * 1024 * 1024
    chunks: list[bytes] = []
    size = 0
    while True:
        chunk = file.file.read(1024 * 1024)
        if not chunk:
            break
        size += len(chunk)
        if size > limit:
            raise HTTPException(
                status_code=413,
                detail="Файл больше {0} МБ".format(settings.max_upload_mb),
            )
        chunks.append(chunk)
    content = b"".join(chunks)

    path, digest = ingest.save_upload(content, name)
    duplicate = ingest.find_duplicate(db, digest)
    if duplicate is not None:
        if not replace:
            path.unlink(missing_ok=True)
            raise HTTPException(
                status_code=409,
                detail="Такой файл уже загружен ({0}, id={1})".format(
                    duplicate.original_name, duplicate.id
                ),
            )
        ingest.remove_file(db, duplicate)

    source = SourceFile(
        original_name=name,
        stored_name=path.name,
        size_bytes=len(content),
        sha256=digest,
        status="pending",
        uploaded_by_id=admin.id,
    )
    db.add(source)
    db.commit()
    db.refresh(source)

    try:
        ingest.ingest_bytes(db, source, content)
    except XmlParseError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc

    db.refresh(source)
    return to_out(source)


@router.post("/{file_id}/reparse", response_model=FileOut)
def reparse_file(
    file_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> FileOut:
    source = db.get(SourceFile, file_id)
    if source is None:
        raise HTTPException(status_code=404, detail="Файл не найден")

    path = settings.storage_dir / source.stored_name
    if not path.exists():
        raise HTTPException(status_code=410, detail="Исходный файл потерян на диске")

    try:
        ingest.ingest_bytes(db, source, path.read_bytes())
    except XmlParseError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    return to_out(source)


@router.get("/{file_id}/download")
def download_file(
    file_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> FileResponse:
    source = db.get(SourceFile, file_id)
    if source is None:
        raise HTTPException(status_code=404, detail="Файл не найден")
    path = settings.storage_dir / source.stored_name
    if not path.exists():
        raise HTTPException(status_code=410, detail="Исходный файл потерян на диске")
    return FileResponse(path, filename=source.original_name, media_type="application/xml")


@router.delete("/{file_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_file(
    file_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> None:
    source = db.get(SourceFile, file_id)
    if source is None:
        raise HTTPException(status_code=404, detail="Файл не найден")
    ingest.remove_file(db, source)
