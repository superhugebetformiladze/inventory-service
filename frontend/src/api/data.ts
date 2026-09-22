/**
 * Запросы за отчётами.
 *
 * Все они принимают один и тот же набор фильтров (какая выгрузка или диапазон дат,
 * режим учёта времени, смена, станки) — он описан типом DataQuery и превращается
 * в строку запроса автоматически.
 */
import type {
  DataQuery,
  Filters,
  Heatmap,
  MachineDetail,
  MachineRow,
  Overview,
  RecordsPage,
  DayRow,
} from '@/types/api'

import { qs, request, saveBlob } from './client'

export const filters = (fileId?: number | null): Promise<Filters> =>
  request<Filters>(`/data/filters${qs({ file_id: fileId })}`)

export const overview = (params: DataQuery): Promise<Overview> =>
  request<Overview>(`/data/overview${qs(params)}`)

export const machines = (params: DataQuery): Promise<MachineRow[]> =>
  request<MachineRow[]>(`/data/machines${qs(params)}`)

export const timeline = (params: DataQuery): Promise<DayRow[]> =>
  request<DayRow[]>(`/data/timeline${qs(params)}`)

export const heatmap = (params: DataQuery): Promise<Heatmap> =>
  request<Heatmap>(`/data/heatmap${qs(params)}`)

export const records = (params: DataQuery): Promise<RecordsPage> =>
  request<RecordsPage>(`/data/records${qs(params)}`)

export const machineDetail = (machineKey: string, params: DataQuery): Promise<MachineDetail> =>
  request<MachineDetail>(
    `/data/machine/${encodeURIComponent(machineKey)}${qs(params)}`,
  )

export async function downloadCsv(params: DataQuery): Promise<void> {
  const response = await request<Response>(
    `/data/export.csv${qs(params)}`,
    { raw: true },
  )
  await saveBlob(await response.blob(), 'monitoring.csv')
}
