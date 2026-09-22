/** Сборка ссылок на страницу станка — с сохранением выбранных фильтров. */
import type { DataQuery } from '@/types/api'

/** Ссылка на страницу станка с текущими фильтрами — чтобы не выбирать их заново. */
export function machineHref(machineKey: string, params: DataQuery | undefined): string {
  const search = new URLSearchParams()
  Object.entries(params ?? {}).forEach(([key, value]) => {
    if (value === undefined || value === null || value === '' || Array.isArray(value)) return
    search.append(key, String(value))
  })
  return `/machine/${encodeURIComponent(machineKey)}?${search.toString()}`
}
