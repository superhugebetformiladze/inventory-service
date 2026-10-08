"""Продукция: номенклатура, привязка к станкам, план и факт выпуска.

Три вещи, которые здесь живут:

1. **Номенклатура** (`/products`) — справочник изделий. Пополняется сам при загрузке
   рапорта и правится руками.
2. **Привязка к станкам** (`/links`) — какое изделие делают на каком станке. В рапорте
   станков нет (он цеховой), поэтому связь заводит администратор.
3. **План и факт** (`/reports`, `/rows`) — рапорт Excel за период или строки, введённые
   вручную. У ручной строки можно указать конкретный станок, у строки из рапорта
   станка нет — только цех. Эту же выборку можно выгрузить обратно в Excel
   (`/export.xlsx`): строки, свод по цехам и свод по изделиям.

Смотреть может любой вошедший пользователь, менять — только администратор.
"""

from datetime import date

from fastapi import APIRouter, Depends, File, HTTPException, Query as Q, UploadFile, status
from fastapi.responses import FileResponse, Response
from sqlalchemy import delete, func, select
from sqlalchemy.orm import Session

from ..config import settings
from ..database import get_db
from ..deps import get_current_user, require_admin
from ..models import MachineProduct, Product, ProductionReport, ProductionRow, User
from ..schemas import (
    MachineLinkIn,
    MachineLinkOut,
    ProductCreate,
    ProductionReportOut,
    ProductionRowCreate,
    ProductionRowOut,
    ProductionRowUpdate,
    ProductOut,
    ProductUpdate,
)
from ..services import production
from ..services.ingest import clean_name
from ..services.production import ReportParseError

router = APIRouter(prefix="/api/production", tags=["production"])


# --------------------------------------------------------------------------------------
# Номенклатура
# --------------------------------------------------------------------------------------


def links_by_product(db: Session) -> dict[int, list[str]]:
    """product_id -> список станков, за которыми закреплено изделие."""
    result: dict[int, list[str]] = {}
    for product_id, machine_key in db.execute(
        select(MachineProduct.product_id, MachineProduct.machine_key)
    ):
        result.setdefault(product_id, []).append(machine_key)
    return result


def product_out(product: Product, machines: list[str]) -> ProductOut:
    return ProductOut(
        id=product.id,
        name=product.name,
        title=product.title,
        body_type=product.body_type,
        workshop=product.workshop,
        code=product.code,
        unit=product.unit,
        notes=product.notes,
        machines=sorted(machines),
    )


@router.get("/products", response_model=list[ProductOut])
def list_products(
    workshop: str | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[ProductOut]:
    stmt = select(Product).order_by(Product.workshop, Product.name)
    if workshop:
        stmt = stmt.where(Product.workshop == workshop)
    bound = links_by_product(db)
    return [product_out(item, bound.get(item.id, [])) for item in db.scalars(stmt)]


@router.get("/workshops", response_model=list[str])
def list_workshops(
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[str]:
    """Цеха, встречающиеся в номенклатуре, — для выпадающих списков."""
    rows = db.scalars(select(Product.workshop).distinct().where(Product.workshop.is_not(None)))
    return sorted(value for value in rows if value)


@router.post("/products", response_model=ProductOut, status_code=status.HTTP_201_CREATED)
def create_product(
    payload: ProductCreate,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> ProductOut:
    key = production.product_key(payload.name, payload.body_type, payload.workshop)
    if db.scalar(select(Product).where(Product.match_key == key)):
        raise HTTPException(
            status_code=409,
            detail="Такое изделие уже есть: название, тип корпуса и цех совпадают",
        )
    product = Product(match_key=key, **payload.model_dump())
    db.add(product)
    db.commit()
    db.refresh(product)
    return product_out(product, [])


@router.patch("/products/{product_id}", response_model=ProductOut)
def update_product(
    product_id: int,
    payload: ProductUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> ProductOut:
    product = db.get(Product, product_id)
    if product is None:
        raise HTTPException(status_code=404, detail="Изделие не найдено")

    for field, value in payload.model_dump(exclude_unset=True).items():
        setattr(product, field, value)

    # Ключ собирается из названия, типа корпуса и цеха — после правки его надо пересобрать,
    # иначе следующая загрузка рапорта заведёт дубль вместо обновления этого изделия.
    key = production.product_key(product.name, product.body_type, product.workshop)
    if key != product.match_key:
        clash = db.scalar(
            select(Product).where(Product.match_key == key, Product.id != product.id)
        )
        if clash is not None:
            raise HTTPException(
                status_code=409,
                detail="Изделие с таким названием, типом корпуса и цехом уже есть",
            )
        product.match_key = key

    db.commit()
    db.refresh(product)
    return product_out(product, links_by_product(db).get(product.id, []))


@router.delete("/products/{product_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_product(
    product_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> None:
    product = db.get(Product, product_id)
    if product is None:
        raise HTTPException(status_code=404, detail="Изделие не найдено")
    # Строки плана и факта и привязки к станкам уходят вместе с изделием (ondelete=CASCADE)
    db.delete(product)
    db.commit()


# --------------------------------------------------------------------------------------
# Привязка изделий к станкам
# --------------------------------------------------------------------------------------


@router.get("/links", response_model=list[MachineLinkOut])
def list_links(
    machine_key: str | None = None,
    product_id: int | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[MachineLinkOut]:
    stmt = select(MachineProduct)
    if machine_key:
        stmt = stmt.where(MachineProduct.machine_key == machine_key)
    if product_id:
        stmt = stmt.where(MachineProduct.product_id == product_id)
    return list(db.scalars(stmt.order_by(MachineProduct.machine_key)))


@router.post("/links", response_model=MachineLinkOut, status_code=status.HTTP_201_CREATED)
def create_link(
    payload: MachineLinkIn,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> MachineLinkOut:
    if db.get(Product, payload.product_id) is None:
        raise HTTPException(status_code=404, detail="Изделие не найдено")
    existing = db.scalar(
        select(MachineProduct).where(
            MachineProduct.machine_key == payload.machine_key,
            MachineProduct.product_id == payload.product_id,
        )
    )
    if existing is not None:
        return existing
    link = MachineProduct(**payload.model_dump())
    db.add(link)
    db.commit()
    db.refresh(link)
    return link


@router.delete("/links/{link_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_link(
    link_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> None:
    link = db.get(MachineProduct, link_id)
    if link is None:
        raise HTTPException(status_code=404, detail="Привязка не найдена")
    db.delete(link)
    db.commit()


# --------------------------------------------------------------------------------------
# Рапорты
# --------------------------------------------------------------------------------------


def report_out(report: ProductionReport) -> ProductionReportOut:
    payload = {
        column.name: getattr(report, column.name)
        for column in ProductionReport.__table__.columns
    }
    payload["uploaded_by"] = report.uploaded_by.username if report.uploaded_by else None
    return ProductionReportOut.model_validate(payload)


@router.get("/reports", response_model=list[ProductionReportOut])
def list_reports(
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[ProductionReportOut]:
    rows = db.scalars(
        select(ProductionReport).order_by(
            ProductionReport.period_from.desc(), ProductionReport.uploaded_at.desc()
        )
    )
    return [report_out(row) for row in rows]


@router.post("/reports/upload", status_code=status.HTTP_201_CREATED)
def upload_report(
    period_from: date,
    period_to: date,
    file: UploadFile = File(...),
    replace: bool = False,
    note: str | None = None,
    db: Session = Depends(get_db),
    admin: User = Depends(require_admin),
) -> dict:
    """Загрузка рапорта за указанный период.

    Периода в самом файле нет, поэтому его задаёт тот, кто загружает. Рапорт за уже
    занятый период не принимается без подтверждения: иначе план и факт задвоятся.
    """
    name = clean_name(file.filename or "рапорт.xlsx")
    if not name.lower().endswith((".xlsx", ".xlsm")):
        raise HTTPException(status_code=400, detail="Ожидается файл Excel (.xlsx)")
    if period_to < period_from:
        raise HTTPException(status_code=400, detail="Конец периода раньше его начала")

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
                status_code=413, detail="Файл больше {0} МБ".format(settings.max_upload_mb)
            )
        chunks.append(chunk)
    content = b"".join(chunks)

    duplicate = production.find_period(db, period_from, period_to)
    if duplicate is not None:
        if not replace:
            raise HTTPException(
                status_code=409,
                detail="Рапорт за этот период уже загружен ({0} от {1})".format(
                    duplicate.original_name, duplicate.uploaded_at.strftime("%d.%m.%Y")
                ),
            )
        production.delete_report(db, duplicate)

    try:
        report, summary = production.import_report(
            db,
            content=content,
            original_name=name,
            period_from=period_from,
            period_to=period_to,
            uploaded_by_id=admin.id,
            note=note,
        )
    except ReportParseError as exc:
        db.rollback()
        raise HTTPException(status_code=400, detail=str(exc)) from exc

    return {"report": report_out(report), "summary": summary.as_dict()}


@router.get("/reports/{report_id}/download")
def download_report(
    report_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> FileResponse:
    report = db.get(ProductionReport, report_id)
    if report is None or not report.stored_name:
        raise HTTPException(status_code=404, detail="Рапорт не найден")
    path = settings.storage_dir / report.stored_name
    if not path.exists():
        raise HTTPException(status_code=404, detail="Файл рапорта не найден на диске")
    return FileResponse(
        path,
        filename=report.original_name,
        media_type="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    )


@router.delete("/reports/{report_id}", status_code=status.HTTP_204_NO_CONTENT)
def remove_report(
    report_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> None:
    report = db.get(ProductionReport, report_id)
    if report is None:
        raise HTTPException(status_code=404, detail="Рапорт не найден")
    production.delete_report(db, report)


# --------------------------------------------------------------------------------------
# Строки плана и факта
# --------------------------------------------------------------------------------------


def row_out(row: ProductionRow) -> ProductionRowOut:
    plan = row.plan_qty
    fact = row.fact_qty
    return ProductionRowOut(
        id=row.id,
        report_id=row.report_id,
        product_id=row.product_id,
        product=row.product.name,
        body_type=row.product.body_type,
        unit=row.product.unit,
        machine_key=row.machine_key,
        period_from=row.period_from,
        period_to=row.period_to,
        plan_qty=plan,
        fact_qty=fact,
        return_qty=row.return_qty,
        percent=round((fact or 0) / plan * 100, 1) if plan else None,
        workshop=row.workshop,
        note=row.note,
        source=row.source,
    )


@router.get("/rows", response_model=list[ProductionRowOut])
def list_rows(
    date_from: date | None = None,
    date_to: date | None = None,
    report_id: int | None = None,
    product_id: int | None = None,
    machine_key: str | None = None,
    workshop: str | None = None,
    search: str | None = None,
    source: str | None = Q(None, pattern="^(file|manual)$"),
    limit: int = Q(500, ge=1, le=5000),
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> list[ProductionRowOut]:
    stmt = select(ProductionRow)
    if date_from:
        stmt = stmt.where(ProductionRow.period_to >= date_from)
    if date_to:
        stmt = stmt.where(ProductionRow.period_from <= date_to)
    if report_id is not None:
        stmt = stmt.where(ProductionRow.report_id == report_id)
    if product_id is not None:
        stmt = stmt.where(ProductionRow.product_id == product_id)
    if machine_key:
        stmt = stmt.where(ProductionRow.machine_key == machine_key)
    if source:
        stmt = stmt.where(ProductionRow.source == source)
    if workshop or search:
        # Цех и название живут в двух таблицах, поэтому для этих фильтров нужна связка
        stmt = stmt.join(Product)
        if workshop:
            # У строки цех может быть не заполнен — тогда берём цех изделия,
            # ровно как это делает выгрузка в Excel
            stmt = stmt.where(
                func.coalesce(ProductionRow.workshop, Product.workshop) == workshop
            )
        if search:
            needle = "%{0}%".format(search.strip().lower())
            stmt = stmt.where(
                func.lower(Product.name).like(needle)
                | func.lower(func.coalesce(Product.body_type, "")).like(needle)
            )
    stmt = stmt.order_by(
        ProductionRow.period_from.desc(), ProductionRow.workshop, ProductionRow.row_no
    ).limit(limit)
    return [row_out(row) for row in db.scalars(stmt)]


@router.post("/rows", response_model=ProductionRowOut, status_code=status.HTTP_201_CREATED)
def create_row(
    payload: ProductionRowCreate,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> ProductionRowOut:
    """Ручной ввод плана и факта. Изделие можно выбрать из справочника или завести здесь же."""
    if payload.period_to < payload.period_from:
        raise HTTPException(status_code=400, detail="Конец периода раньше его начала")

    if payload.product_id is not None:
        product = db.get(Product, payload.product_id)
        if product is None:
            raise HTTPException(status_code=404, detail="Изделие не найдено")
    elif payload.product_name:
        cache = {item.match_key: item for item in db.scalars(select(Product))}
        product = production.get_or_create_product(
            db, cache, payload.product_name, payload.body_type, payload.workshop
        )
    else:
        raise HTTPException(
            status_code=400, detail="Нужно выбрать изделие или указать его название"
        )

    row = ProductionRow(
        product_id=product.id,
        machine_key=payload.machine_key,
        period_from=payload.period_from,
        period_to=payload.period_to,
        plan_qty=payload.plan_qty,
        fact_qty=payload.fact_qty,
        return_qty=payload.return_qty,
        workshop=payload.workshop or product.workshop,
        note=payload.note,
        source="manual",
    )
    db.add(row)
    db.commit()
    db.refresh(row)
    return row_out(row)


@router.patch("/rows/{row_id}", response_model=ProductionRowOut)
def update_row(
    row_id: int,
    payload: ProductionRowUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> ProductionRowOut:
    row = db.get(ProductionRow, row_id)
    if row is None:
        raise HTTPException(status_code=404, detail="Строка не найдена")
    for field, value in payload.model_dump(exclude_unset=True).items():
        setattr(row, field, value)
    if row.period_to < row.period_from:
        raise HTTPException(status_code=400, detail="Конец периода раньше его начала")
    db.commit()
    db.refresh(row)
    return row_out(row)


@router.delete("/rows/{row_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_row(
    row_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(require_admin),
) -> None:
    row = db.get(ProductionRow, row_id)
    if row is None:
        raise HTTPException(status_code=404, detail="Строка не найдена")
    db.delete(row)
    db.commit()


# --------------------------------------------------------------------------------------
# Сводки
# --------------------------------------------------------------------------------------


@router.get("/summary")
def summary(
    date_from: date | None = None,
    date_to: date | None = None,
    workshop: str | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> dict:
    """Итог по всей продукции за период: план, факт, процент и разрез по цехам."""
    rows = production.rows_in_period(db, date_from, date_to)
    if workshop:
        rows = [row for row in rows if (row.workshop or row.product.workshop) == workshop]

    shops: dict[str, list] = {}
    for row in rows:
        shops.setdefault(row.workshop or row.product.workshop or "Без цеха", []).append(row)

    return {
        "totals": production.aggregate(rows),
        "by_product": production.by_product(rows)[:50],
        "by_workshop": sorted(
            (
                {"workshop": name, **production.aggregate(items)}
                for name, items in shops.items()
            ),
            key=lambda item: -item["plan_qty"],
        ),
        "products_total": db.scalar(select(func.count(Product.id))) or 0,
        "linked_machines": db.scalar(
            select(func.count(func.distinct(MachineProduct.machine_key)))
        )
        or 0,
    }


@router.get("/export.xlsx")
def export_xlsx(
    date_from: date | None = None,
    date_to: date | None = None,
    workshop: str | None = None,
    search: str | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> Response:
    """Выгрузка плана и факта в Excel — ровно та выборка, что видна на странице."""
    rows = production.rows_in_period(db, date_from, date_to)
    if workshop:
        rows = [row for row in rows if (row.workshop or row.product.workshop) == workshop]
    if search:
        needle = search.strip().lower()
        rows = [
            row
            for row in rows
            if needle in row.product.name.lower()
            or needle in (row.product.body_type or "").lower()
        ]

    content = production.build_workbook(db, rows, date_from, date_to)
    stamp = date.today().strftime("%Y-%m-%d")
    name = "plan-fact-{0}.xlsx".format(stamp)
    return Response(
        content=content,
        media_type="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        headers={"Content-Disposition": 'attachment; filename="{0}"'.format(name)},
    )


@router.get("/machine/{machine_key:path}")
def machine_production(
    machine_key: str,
    date_from: date | None = None,
    date_to: date | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(get_current_user),
) -> dict:
    """Продукция одного станка за период — блок на странице станка."""
    return production.machine_production(db, machine_key, date_from, date_to)
