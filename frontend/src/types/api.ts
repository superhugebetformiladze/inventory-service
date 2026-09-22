/** Типы ответов бэкенда. Имена полей совпадают с JSON как есть (snake_case). */

export type Role = 'admin' | 'viewer'

export interface User {
  id: number
  username: string
  full_name: string | null
  role: Role
  is_active: boolean
  created_at: string | null
}

export interface SourceFile {
  id: number
  original_name: string
  size_bytes: number
  status: 'pending' | 'ok' | 'error'
  error: string | null
  uploaded_at: string
  uploaded_by: string | null
  replication_name: string | null
  replication_dt: string | null
  monitoring_from: string | null
  monitoring_to: string | null
  machines_count: number
  params_count: number
  records_count: number
  date_min: string | null
  date_max: string | null
}

export interface MachineOption {
  key: string
  name: string
  alias: string | null
}

export interface ParamOption {
  code: string | null
  name: string
  color: string | null
  group_name: string | null
}

export interface Filters {
  machines: MachineOption[]
  params: ParamOption[]
  groups: string[]
  date_min: string | null
  date_max: string | null
  shifts: number[]
}

export interface Totals {
  total_seconds: number
  work_seconds: number
  off_seconds: number
  powered_seconds: number
  downtime_seconds: number
  utilization: number
  utilization_powered: number
  records: number
  events: number
  machines: number
  days: number
  date_min: string | null
  date_max: string | null
}

export interface GroupShare {
  group: string
  ext_id: number | null
  seconds: number
  share: number
}

export interface ParamShare {
  name: string
  code: string | null
  color: string | null
  group: string
  seconds: number
  share: number
  events: number
}

export interface Overview {
  scope: string
  schedule_type: number
  shift: number | null
  totals: Totals
  by_group: GroupShare[]
  by_param: ParamShare[]
}

export interface MachineRow {
  key: string
  name: string
  total_seconds: number
  work_seconds: number
  off_seconds: number
  downtime_seconds: number
  utilization: number
  utilization_powered: number
  groups: Record<string, number>
}

export interface DayRow {
  date: string
  total_seconds: number
  work_seconds: number
  off_seconds: number
  downtime_seconds: number
  utilization: number
}

export interface HeatmapCell {
  machine: string
  date: string
  utilization: number
  work_seconds: number
  total_seconds: number
}

export interface Heatmap {
  dates: string[]
  machines: { key: string; name: string }[]
  cells: HeatmapCell[]
}

export interface MonitoringRecordRow {
  id: number
  machine: string
  machine_key: string
  param: string
  code: string | null
  color: string | null
  group: string
  calc_date: string
  from_date: string | null
  to_date: string | null
  shift: number | null
  seconds: number
  events: number
}

export interface RecordsPage {
  total: number
  limit: number
  offset: number
  items: MonitoringRecordRow[]
}

export type BindingKind = 'auto' | 'guess' | 'manual' | 'none'

export interface EquipmentCard {
  id: number
  inv_number: string
  title: string
  mdm_code: string | null
  monitoring_id: number | null
  ip_address: string | null
  cnc_name: string | null
  cnc_type: string | null
  shop_number: string | null
  row_no: number | null
  workshop: string | null
  name: string | null
  model: string | null
  cnc_system: string | null
  import_note: string | null
  display_name: string | null
  serial_number: string | null
  manufacturer: string | null
  year_made: number | null
  location: string | null
  responsible: string | null
  commissioned_on: string | null
  status: string | null
  notes: string | null
  machine_key: string | null
  binding_kind: BindingKind
  has_data: boolean
}

export interface MachineBinding {
  machine_key: string
  mdm_code: string
  card_id: number | null
  title: string
  binding_kind: BindingKind
}

export interface ImportReport {
  sheets: string[]
  created: number
  updated: number
  skipped: string[]
  bound_exact: number
  bound_guess: number
  cards_without_machine: string[]
  machines_without_card: string[]
  unknown_machines: string[]
}

export interface FleetSummary {
  utilization: number
  average_utilization: number
  machines: number
  position: number | null
  best_day: DayRow | null
  worst_day: DayRow | null
  idle_days: number
}

export interface MachineDetail {
  machine_key: string
  title: string
  card: EquipmentCard | null
  totals: Totals
  by_group: GroupShare[]
  by_param: ParamShare[]
  by_day: DayRow[]
  machine: MachineRow | null
  fleet: FleetSummary
}

/** Параметры выборки, общие для всех отчётов. */
export interface DataQuery {
  scope: 'file' | 'range'
  file_id?: number | null
  date_from?: string
  date_to?: string
  schedule_type: number
  shift?: number | string
  machines?: string[]
  limit?: number
  offset?: number
}
