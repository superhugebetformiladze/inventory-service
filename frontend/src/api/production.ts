/**
 * Продукция: справочник изделий, привязка к станкам, план и факт выпуска.
 *
 * План и факт попадают в систему двумя путями: загрузкой рапорта Excel (в нём нет
 * ни станков, ни дат — период указывает тот, кто загружает) и ручным вводом строк,
 * где станок как раз можно указать.
 */
import type {
  MachineLink,
  MachineProduction,
  Product,
  ProductionReport,
  ProductionRow,
  ProductionSummary,
  ProductionUploadResult,
} from '@/types/api'

import { qs, request } from './client'

export type ProductPatch = Partial<
  Pick<Product, 'name' | 'body_type' | 'workshop' | 'code' | 'unit' | 'notes'>
>

/** Параметры выборки строк плана и факта. */
export interface RowsQuery {
  date_from?: string
  date_to?: string
  report_id?: number
  product_id?: number
  machine_key?: string
  workshop?: string
  /** Поиск по названию изделия и типу корпуса. */
  search?: string
  source?: 'file' | 'manual'
  limit?: number
}

/** Ручной ввод строки: изделие либо выбирается из справочника, либо заводится тут же. */
export interface RowInput {
  product_id?: number
  product_name?: string
  body_type?: string | null
  workshop?: string | null
  machine_key?: string | null
  period_from: string
  period_to: string
  plan_qty?: number | null
  fact_qty?: number | null
  return_qty?: number | null
  note?: string | null
}

/* --- Номенклатура --- */

export const products = (workshop?: string): Promise<Product[]> =>
  request<Product[]>(`/production/products${qs({ workshop })}`)

export const workshops = (): Promise<string[]> => request<string[]>('/production/workshops')

export const createProduct = (body: ProductPatch): Promise<Product> =>
  request<Product>('/production/products', { method: 'POST', body })

export const updateProduct = (id: number, body: ProductPatch): Promise<Product> =>
  request<Product>(`/production/products/${id}`, { method: 'PATCH', body })

export const removeProduct = (id: number): Promise<void> =>
  request<void>(`/production/products/${id}`, { method: 'DELETE' })

/* --- Привязка изделий к станкам --- */

export const links = (params?: { machine_key?: string; product_id?: number }): Promise<MachineLink[]> =>
  request<MachineLink[]>(`/production/links${qs(params)}`)

export const link = (machine_key: string, product_id: number): Promise<MachineLink> =>
  request<MachineLink>('/production/links', { method: 'POST', body: { machine_key, product_id } })

export const unlink = (id: number): Promise<void> =>
  request<void>(`/production/links/${id}`, { method: 'DELETE' })

/* --- Рапорты --- */

export const reports = (): Promise<ProductionReport[]> =>
  request<ProductionReport[]>('/production/reports')

export function upload(
  file: File,
  periodFrom: string,
  periodTo: string,
  replace = false,
): Promise<ProductionUploadResult> {
  const form = new FormData()
  form.append('file', file)
  return request<ProductionUploadResult>(
    `/production/reports/upload${qs({
      period_from: periodFrom,
      period_to: periodTo,
      replace: replace || undefined,
    })}`,
    { method: 'POST', form },
  )
}

export const removeReport = (id: number): Promise<void> =>
  request<void>(`/production/reports/${id}`, { method: 'DELETE' })

export async function download(id: number, name: string): Promise<void> {
  const response = await request<Response>(`/production/reports/${id}/download`, { raw: true })
  const { saveBlob } = await import('./client')
  await saveBlob(await response.blob(), name)
}

/* --- Строки плана и факта --- */

export const rows = (params?: RowsQuery): Promise<ProductionRow[]> =>
  request<ProductionRow[]>(`/production/rows${qs(params)}`)

export const createRow = (body: RowInput): Promise<ProductionRow> =>
  request<ProductionRow>('/production/rows', { method: 'POST', body })

export const updateRow = (id: number, body: Partial<RowInput>): Promise<ProductionRow> =>
  request<ProductionRow>(`/production/rows/${id}`, { method: 'PATCH', body })

export const removeRow = (id: number): Promise<void> =>
  request<void>(`/production/rows/${id}`, { method: 'DELETE' })

/** Выгрузка текущей выборки в Excel: строки, свод по цехам и свод по изделиям. */
export async function exportXlsx(params?: {
  date_from?: string
  date_to?: string
  workshop?: string
  search?: string
}): Promise<void> {
  const response = await request<Response>(`/production/export.xlsx${qs(params)}`, { raw: true })
  const { saveBlob } = await import('./client')
  const stamp = new Date().toISOString().slice(0, 10)
  await saveBlob(await response.blob(), `план-факт-${stamp}.xlsx`)
}

/* --- Сводки --- */

export const summary = (params?: {
  date_from?: string
  date_to?: string
  workshop?: string
}): Promise<ProductionSummary> => request<ProductionSummary>(`/production/summary${qs(params)}`)

export const machine = (
  machineKey: string,
  params?: { date_from?: string; date_to?: string },
): Promise<MachineProduction> =>
  request<MachineProduction>(
    `/production/machine/${encodeURIComponent(machineKey)}${qs(params)}`,
  )
