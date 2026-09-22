"""Таблицы базы данных.

Каждый класс здесь — это таблица, а его поля — колонки. Такой способ описания
(ORM, SQLAlchemy) позволяет работать с данными как с обычными объектами Python,
а SQL-запросы библиотека составляет сама.

Что где лежит:
  * User            — пользователи системы и их роли;
  * SourceFile      — загруженные XML-выгрузки и сводка по каждой;
  * FileMachine / FileGroup / FileParam — справочники из конкретного файла;
  * MonitoringRecord — главные данные: сколько времени станок провёл в каждом состоянии;
  * Shift           — графики смен из файла;
  * EquipmentCard   — паспорт станка: инвентарный номер, модель, IP и прочее.
"""

from datetime import date, datetime

from sqlalchemy import (
    BigInteger,
    Boolean,
    Date,
    DateTime,
    Float,
    ForeignKey,
    Index,
    Integer,
    String,
    Text,
    func,
)
from sqlalchemy.orm import Mapped, mapped_column, relationship

from .database import Base


class User(Base):
    """Учётная запись: кто заходит в систему и с какими правами."""

    __tablename__ = "users"

    id: Mapped[int] = mapped_column(primary_key=True)
    username: Mapped[str] = mapped_column(String(64), unique=True, index=True)
    full_name: Mapped[str | None] = mapped_column(String(128))
    hashed_password: Mapped[str] = mapped_column(String(255))
    role: Mapped[str] = mapped_column(String(16), default="viewer")  # admin | viewer
    is_active: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())

    @property
    def is_admin(self) -> bool:
        return self.role == "admin"


class SourceFile(Base):
    """Загруженная XML-выгрузка: откуда взялась, за какой период и что в ней нашлось."""

    __tablename__ = "source_files"

    id: Mapped[int] = mapped_column(primary_key=True)
    original_name: Mapped[str] = mapped_column(String(512))
    stored_name: Mapped[str] = mapped_column(String(512))
    size_bytes: Mapped[int] = mapped_column(BigInteger, default=0)
    sha256: Mapped[str] = mapped_column(String(64), index=True)
    status: Mapped[str] = mapped_column(String(16), default="pending")  # pending|ok|error
    error: Mapped[str | None] = mapped_column(Text)

    uploaded_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())
    uploaded_by_id: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"))
    uploaded_by = relationship("User", lazy="joined")

    replication_name: Mapped[str | None] = mapped_column(String(255))
    replication_dt: Mapped[datetime | None] = mapped_column(DateTime)
    monitoring_from: Mapped[datetime | None] = mapped_column(DateTime, index=True)
    monitoring_to: Mapped[datetime | None] = mapped_column(DateTime, index=True)

    machines_count: Mapped[int] = mapped_column(Integer, default=0)
    params_count: Mapped[int] = mapped_column(Integer, default=0)
    records_count: Mapped[int] = mapped_column(Integer, default=0)
    date_min: Mapped[datetime | None] = mapped_column(DateTime)
    date_max: Mapped[datetime | None] = mapped_column(DateTime)


class FileMachine(Base):
    """Станки, перечисленные в конкретной выгрузке."""

    __tablename__ = "file_machines"

    id: Mapped[int] = mapped_column(primary_key=True)
    file_id: Mapped[int] = mapped_column(ForeignKey("source_files.id", ondelete="CASCADE"), index=True)
    ext_id: Mapped[int] = mapped_column(Integer)
    integration_id: Mapped[str] = mapped_column(String(128), index=True)


class FileGroup(Base):
    """Группы состояний из выгрузки: машинное время, простои, техобслуживание."""

    __tablename__ = "file_groups"

    id: Mapped[int] = mapped_column(primary_key=True)
    file_id: Mapped[int] = mapped_column(ForeignKey("source_files.id", ondelete="CASCADE"), index=True)
    ext_id: Mapped[int] = mapped_column(Integer)
    name: Mapped[str] = mapped_column(String(255))


class FileParam(Base):
    """Состояния станка из выгрузки: работа по программе, наладка, авария и прочие."""

    __tablename__ = "file_params"

    id: Mapped[int] = mapped_column(primary_key=True)
    file_id: Mapped[int] = mapped_column(ForeignKey("source_files.id", ondelete="CASCADE"), index=True)
    ext_id: Mapped[int] = mapped_column(Integer)
    name: Mapped[str] = mapped_column(String(255))
    type_id: Mapped[int | None] = mapped_column(Integer)
    integration_id: Mapped[str | None] = mapped_column(String(64))
    color: Mapped[str | None] = mapped_column(String(16))
    group_ext_id: Mapped[int | None] = mapped_column(Integer)
    group_name: Mapped[str | None] = mapped_column(String(255))


class MonitoringRecord(Base):
    """Плоская таблица интервалов состояний — денормализована ради быстрых сводок."""

    __tablename__ = "monitoring_records"

    id: Mapped[int] = mapped_column(primary_key=True)
    file_id: Mapped[int] = mapped_column(ForeignKey("source_files.id", ondelete="CASCADE"), index=True)
    ext_id: Mapped[int] = mapped_column(BigInteger)

    machine_ext_id: Mapped[int] = mapped_column(Integer)
    machine_key: Mapped[str] = mapped_column(String(128), index=True)

    param_ext_id: Mapped[int] = mapped_column(Integer)
    param_name: Mapped[str] = mapped_column(String(255))
    param_code: Mapped[str | None] = mapped_column(String(64))
    param_color: Mapped[str | None] = mapped_column(String(16))
    group_ext_id: Mapped[int | None] = mapped_column(Integer)
    group_name: Mapped[str | None] = mapped_column(String(255))

    from_date: Mapped[datetime | None] = mapped_column(DateTime)
    to_date: Mapped[datetime | None] = mapped_column(DateTime)
    calc_date: Mapped[datetime] = mapped_column(DateTime, index=True)
    seconds: Mapped[float] = mapped_column(Float, default=0.0)
    smena_num: Mapped[int | None] = mapped_column(Integer)
    count_state: Mapped[int] = mapped_column(Integer, default=0)
    schedule_type_id: Mapped[int] = mapped_column(Integer, index=True)


Index("ix_records_file_calc", MonitoringRecord.file_id, MonitoringRecord.calc_date)
Index("ix_records_scope", MonitoringRecord.schedule_type_id, MonitoringRecord.calc_date)


class Shift(Base):
    """График смен: когда станок должен был работать по плану."""

    __tablename__ = "shifts"

    id: Mapped[int] = mapped_column(primary_key=True)
    file_id: Mapped[int] = mapped_column(ForeignKey("source_files.id", ondelete="CASCADE"), index=True)
    machine_ext_id: Mapped[int] = mapped_column(Integer)
    machine_key: Mapped[str] = mapped_column(String(128), index=True)
    fix_date: Mapped[datetime | None] = mapped_column(DateTime, index=True)
    smena_num: Mapped[int | None] = mapped_column(Integer)
    name: Mapped[str | None] = mapped_column(String(255))
    time_from: Mapped[float | None] = mapped_column(Float)
    time_to: Mapped[float | None] = mapped_column(Float)
    is_holiday: Mapped[bool] = mapped_column(Boolean, default=False)


class EquipmentCard(Base):
    """Карточка оборудования: паспортные данные станка.

    В XML-выгрузке о станке известен только IntegrationID вида
    MDM-C19-002147500&36, где префикс — код MDM (общий для одинаковых моделей),
    а суффикс — идентификатор станка в системе мониторинга. Наименование, модель,
    инвентарный номер и прочее живут в таблице оборудования (Excel) и здесь.

    Ключ карточки — инвентарный номер: он уникален, в отличие от кода MDM.
    """

    __tablename__ = "equipment_cards"

    id: Mapped[int] = mapped_column(primary_key=True)
    inv_number: Mapped[str] = mapped_column(String(64), unique=True, index=True)

    # Из таблицы оборудования
    mdm_code: Mapped[str | None] = mapped_column(String(128), index=True)
    monitoring_id: Mapped[int | None] = mapped_column(Integer, index=True)
    ip_address: Mapped[str | None] = mapped_column(String(64))
    cnc_name: Mapped[str | None] = mapped_column(String(128))
    cnc_type: Mapped[str | None] = mapped_column(String(255))
    shop_number: Mapped[str | None] = mapped_column(String(32))
    row_no: Mapped[int | None] = mapped_column(Integer)
    workshop: Mapped[str | None] = mapped_column(String(64))
    name: Mapped[str | None] = mapped_column(String(255))
    model: Mapped[str | None] = mapped_column(String(255))
    cnc_system: Mapped[str | None] = mapped_column(String(255))
    import_note: Mapped[str | None] = mapped_column(Text)

    # Заполняется руками в админке
    display_name: Mapped[str | None] = mapped_column(String(255))
    serial_number: Mapped[str | None] = mapped_column(String(128))
    manufacturer: Mapped[str | None] = mapped_column(String(255))
    year_made: Mapped[int | None] = mapped_column(Integer)
    location: Mapped[str | None] = mapped_column(String(255))
    responsible: Mapped[str | None] = mapped_column(String(255))
    commissioned_on: Mapped[date | None] = mapped_column(Date)
    status: Mapped[str | None] = mapped_column(String(64))
    notes: Mapped[str | None] = mapped_column(Text)

    # Привязка к станку из мониторинга
    machine_key: Mapped[str | None] = mapped_column(String(128), unique=True, index=True)
    binding_kind: Mapped[str] = mapped_column(String(16), default="none")  # auto|guess|manual|none

    created_at: Mapped[datetime] = mapped_column(DateTime, server_default=func.now())
    updated_at: Mapped[datetime | None] = mapped_column(DateTime, onupdate=func.now())

    @property
    def title(self) -> str:
        """Как подписывать станок в отчётах."""
        if self.display_name:
            return self.display_name
        parts = [self.model or self.name or "", "№{0}".format(self.inv_number) if self.inv_number else ""]
        return " ".join(part for part in parts if part).strip() or (self.machine_key or "")
