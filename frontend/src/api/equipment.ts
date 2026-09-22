/** Карточки оборудования: чтение, правка, импорт из Excel, привязка к станкам. */
import type { EquipmentCard, ImportReport, MachineBinding } from '@/types/api'

import { request } from './client'

/** Поля карточки, доступные для правки вручную. */
export type EquipmentPatch = Partial<
  Pick<
    EquipmentCard,
    | 'inv_number'
    | 'mdm_code'
    | 'monitoring_id'
    | 'ip_address'
    | 'cnc_name'
    | 'cnc_type'
    | 'shop_number'
    | 'workshop'
    | 'name'
    | 'model'
    | 'cnc_system'
    | 'display_name'
    | 'serial_number'
    | 'manufacturer'
    | 'year_made'
    | 'location'
    | 'responsible'
    | 'commissioned_on'
    | 'status'
    | 'notes'
    | 'machine_key'
  >
>

export const list = (): Promise<EquipmentCard[]> => request<EquipmentCard[]>('/equipment')

export const machines = (): Promise<MachineBinding[]> =>
  request<MachineBinding[]>('/equipment/machines')

export const create = (body: EquipmentPatch): Promise<EquipmentCard> =>
  request<EquipmentCard>('/equipment', { method: 'POST', body })

export const update = (id: number, body: EquipmentPatch): Promise<EquipmentCard> =>
  request<EquipmentCard>(`/equipment/${id}`, { method: 'PATCH', body })

export const remove = (id: number): Promise<void> =>
  request<void>(`/equipment/${id}`, { method: 'DELETE' })

export const rebind = (): Promise<ImportReport> =>
  request<ImportReport>('/equipment/rebind', { method: 'POST' })

export function importExcel(file: File): Promise<ImportReport> {
  const form = new FormData()
  form.append('file', file)
  return request<ImportReport>('/equipment/import', { method: 'POST', form })
}
