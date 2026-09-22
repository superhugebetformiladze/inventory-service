/**
 * Раздел админки «Файлы»: загрузка XML-выгрузок и список загруженного.
 *
 * Файл можно перетащить мышью в рамку или выбрать на диске. Если такой файл уже
 * загружали, сервер сообщит об этом, и мы спросим, перезаписать ли данные.
 */
import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, filesApi } from '@/api'
import { Alert, Card, Empty, Loader } from '@/components/ui'
import { formatDate, formatDateTime, formatNumber, formatSize } from '@/lib/format'
import type { SourceFile } from '@/types/api'

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function FilesPanel() {
  const [files, setFiles] = useState<SourceFile[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [info, setInfo] = useState('')
  const [busy, setBusy] = useState(false)
  const [dragOver, setDragOver] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setFiles(await filesApi.list())
    } catch (err) {
      setError(message(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function upload(file: File | undefined, replace = false): Promise<void> {
    if (!file) return
    setError('')
    setInfo('')
    setBusy(true)
    try {
      const result = await filesApi.upload(file, replace)
      setInfo(
        `Файл «${result.original_name}» загружен: ${formatNumber(result.records_count)} записей, ` +
          `${result.machines_count} станков.`,
      )
      await load()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        if (window.confirm(`${err.message}. Перезаписать данными из нового файла?`)) {
          await upload(file, true)
          return
        }
      } else {
        setError(message(err))
      }
    } finally {
      setBusy(false)
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  async function reparse(id: number): Promise<void> {
    setBusy(true)
    try {
      await filesApi.reparse(id)
      setInfo('Файл перечитан заново.')
      await load()
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }

  async function remove(file: SourceFile): Promise<void> {
    if (!window.confirm(`Удалить «${file.original_name}» и все его данные?`)) return
    setBusy(true)
    try {
      await filesApi.remove(file.id)
      await load()
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Alert onClose={() => setError('')}>{error}</Alert>
      <Alert kind="success" onClose={() => setInfo('')}>
        {info}
      </Alert>

      <Card title="Загрузка выгрузки" subtitle="XML-файл формата XmlDataWrapper">
        <div
          className={`dropzone ${dragOver ? 'over' : ''}`}
          onDragOver={(event) => {
            event.preventDefault()
            setDragOver(true)
          }}
          onDragLeave={() => setDragOver(false)}
          onDrop={(event) => {
            event.preventDefault()
            setDragOver(false)
            void upload(event.dataTransfer.files?.[0])
          }}
          onClick={() => inputRef.current?.click()}
        >
          <input
            ref={inputRef}
            type="file"
            accept=".xml"
            hidden
            onChange={(event) => void upload(event.target.files?.[0])}
          />
          <strong>{busy ? 'Обрабатываем файл…' : 'Перетащите XML сюда'}</strong>
          <span>или нажмите, чтобы выбрать файл на диске</span>
        </div>
      </Card>

      <Card title="Загруженные файлы" className="wide">
        {loading ? (
          <Loader />
        ) : files.length === 0 ? (
          <Empty>Пока ничего не загружено</Empty>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Файл</th>
                  <th>Период данных</th>
                  <th>Сформирован</th>
                  <th className="num">Записей</th>
                  <th className="num">Станков</th>
                  <th className="num">Размер</th>
                  <th>Загрузил</th>
                  <th>Статус</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {files.map((file) => (
                  <tr key={file.id}>
                    <td className="ellipsis" title={file.original_name}>
                      {file.original_name}
                    </td>
                    <td>
                      {formatDate(file.monitoring_from)} — {formatDate(file.monitoring_to)}
                    </td>
                    <td className="muted">{formatDateTime(file.replication_dt)}</td>
                    <td className="num">{formatNumber(file.records_count)}</td>
                    <td className="num">{file.machines_count}</td>
                    <td className="num muted">{formatSize(file.size_bytes)}</td>
                    <td className="muted">
                      {file.uploaded_by || '—'}
                      <div className="sub">{formatDateTime(file.uploaded_at)}</div>
                    </td>
                    <td>
                      <span className={`status status-${file.status}`}>
                        {file.status === 'ok' ? 'разобран' : file.status}
                      </span>
                      {file.error && <div className="sub error">{file.error}</div>}
                    </td>
                    <td className="row-actions">
                      <button
                        type="button"
                        className="btn link"
                        onClick={() => void filesApi.download(file.id, file.original_name)}
                      >
                        скачать
                      </button>
                      <button type="button" className="btn link" onClick={() => void reparse(file.id)}>
                        перечитать
                      </button>
                      <button
                        type="button"
                        className="btn link danger"
                        onClick={() => void remove(file)}
                      >
                        удалить
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  )
}
