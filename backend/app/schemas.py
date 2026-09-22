"""Описание данных, которые API принимает и отдаёт.

Это не таблицы базы, а «анкеты» для запросов и ответов (библиотека Pydantic).
Каждая такая анкета говорит, какие поля обязательны и какого они типа. Если клиент
пришлёт пароль короче восьми символов или текст вместо числа, запрос будет отклонён
с понятной ошибкой ещё до того, как дойдёт до нашего кода.

Обратная сторона: в ответ уходят только перечисленные здесь поля — случайно отдать
наружу хеш пароля или внутреннее поле невозможно.
"""

from datetime import date, datetime

from pydantic import BaseModel, ConfigDict, Field


class Token(BaseModel):
    access_token: str
    token_type: str = "bearer"


class LoginRequest(BaseModel):
    username: str
    password: str


class UserOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    username: str
    full_name: str | None = None
    role: str
    is_active: bool
    created_at: datetime | None = None


class UserCreate(BaseModel):
    username: str = Field(min_length=3, max_length=64)
    password: str = Field(min_length=8, max_length=128)
    full_name: str | None = None
    role: str = "viewer"


class UserUpdate(BaseModel):
    password: str | None = Field(default=None, min_length=8, max_length=128)
    full_name: str | None = None
    role: str | None = None
    is_active: bool | None = None


class FileOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    original_name: str
    size_bytes: int
    status: str
    error: str | None = None
    uploaded_at: datetime
    uploaded_by: str | None = None
    replication_name: str | None = None
    replication_dt: datetime | None = None
    monitoring_from: datetime | None = None
    monitoring_to: datetime | None = None
    machines_count: int
    params_count: int
    records_count: int
    date_min: datetime | None = None
    date_max: datetime | None = None


class MachineOut(BaseModel):
    key: str
    name: str
    alias: str | None = None


class ParamOut(BaseModel):
    code: str | None = None
    name: str
    color: str | None = None
    group_name: str | None = None


class FiltersOut(BaseModel):
    machines: list[MachineOut]
    params: list[ParamOut]
    groups: list[str]
    date_min: datetime | None = None
    date_max: datetime | None = None
    shifts: list[int]


class EquipmentOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    inv_number: str
    title: str
    mdm_code: str | None = None
    monitoring_id: int | None = None
    ip_address: str | None = None
    cnc_name: str | None = None
    cnc_type: str | None = None
    shop_number: str | None = None
    row_no: int | None = None
    workshop: str | None = None
    name: str | None = None
    model: str | None = None
    cnc_system: str | None = None
    import_note: str | None = None
    display_name: str | None = None
    serial_number: str | None = None
    manufacturer: str | None = None
    year_made: int | None = None
    location: str | None = None
    responsible: str | None = None
    commissioned_on: date | None = None
    status: str | None = None
    notes: str | None = None
    machine_key: str | None = None
    binding_kind: str = "none"
    has_data: bool = False


class EquipmentCreate(BaseModel):
    inv_number: str = Field(min_length=1, max_length=64)
    mdm_code: str | None = None
    monitoring_id: int | None = None
    ip_address: str | None = None
    cnc_name: str | None = None
    cnc_type: str | None = None
    shop_number: str | None = None
    workshop: str | None = None
    name: str | None = None
    model: str | None = None
    cnc_system: str | None = None
    display_name: str | None = None
    serial_number: str | None = None
    manufacturer: str | None = None
    year_made: int | None = Field(default=None, ge=1900, le=2100)
    location: str | None = None
    responsible: str | None = None
    commissioned_on: date | None = None
    status: str | None = None
    notes: str | None = None


class EquipmentUpdate(BaseModel):
    inv_number: str | None = Field(default=None, min_length=1, max_length=64)
    mdm_code: str | None = None
    monitoring_id: int | None = None
    ip_address: str | None = None
    cnc_name: str | None = None
    cnc_type: str | None = None
    shop_number: str | None = None
    workshop: str | None = None
    name: str | None = None
    model: str | None = None
    cnc_system: str | None = None
    display_name: str | None = None
    serial_number: str | None = None
    manufacturer: str | None = None
    year_made: int | None = Field(default=None, ge=1900, le=2100)
    location: str | None = None
    responsible: str | None = None
    commissioned_on: date | None = None
    status: str | None = None
    notes: str | None = None
    import_note: str | None = None
    machine_key: str | None = None
