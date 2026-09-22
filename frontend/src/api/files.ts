/** XML-выгрузки: список, загрузка, повторный разбор, скачивание, удаление. */
import type { SourceFile } from '@/types/api'

import { qs, request, saveBlob } from './client'

export const list = (): Promise<SourceFile[]> => request<SourceFile[]>('/files')

export function upload(file: File, replace = false): Promise<SourceFile> {
  const form = new FormData()
  form.append('file', file)
  return request<SourceFile>(`/files/upload${qs({ replace })}`, { method: 'POST', form })
}

export const reparse = (id: number): Promise<SourceFile> =>
  request<SourceFile>(`/files/${id}/reparse`, { method: 'POST' })

export const remove = (id: number): Promise<void> => request<void>(`/files/${id}`, { method: 'DELETE' })

export async function download(id: number, name?: string): Promise<void> {
  const response = await request<Response>(`/files/${id}/download`, { raw: true })
  await saveBlob(await response.blob(), name || `file-${id}.xml`)
}
