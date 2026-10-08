/**
 * Загрузка рапортов и просмотр плана с фактом.
 *
 * Рапорт — это «Справка о выполнении плана производства». В самом файле не написано,
 * за какой он период, поэтому период указывается здесь же, перед загрузкой. Рапорт за
 * уже занятый период сервер не примет без подтверждения: иначе план и факт задвоятся.
 *
 * Ниже — строки выпуска: за выбранный период или за всё время. Показываются и те,
 * что пришли из рапортов, и те, что заведены вручную (у ручных можно указать станок).
 */
import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, productionApi } from '@/api'
import { Alert, Card, Empty, Loader } from '@/components/ui'
import { formatDate, formatDateTime, formatNumber, formatSize } from '@/lib/format'
import type { ProductionReport, ProductionRow, ReportImportSummary } from '@/types/api'

import { RowDialog } from './RowDialog'

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

/** Первое и последнее число месяца, в котором лежит дата. */
function monthBounds(base = new Date()): [string, string] {
  const year = base.getFullYear()
  const month = base.getMonth()
  const iso = (value: Date) =>
    new Date(value.getTime() - value.getTimezoneOffset() * 60000).toISOString().slice(0, 10)
  return [iso(new Date(year, month, 1)), iso(new Date(year, month + 1, 0))]
}

/** Процент выполнения: факт ÷ план. Без плана процента не существует. */
function percent(fact: number | null, plan: number | null): string {
  if (!plan) return '—'
  return `${(((fact || 0) / plan) * 100).toFixed(1)} %`
}

export function ReportsSection() {
  const [defaultFrom, defaultTo] = monthBounds()

  const [reports, setReports] = useState<ProductionReport[]>([])
  const [rows, setRows] = useState<ProductionRow[]>([])
  const [summary, setSummary] = useState<ReportImportSummary | null>(null)

  const [periodFrom, setPeriodFrom] = useState(defaultFrom)
  const [periodTo, setPeriodTo] = useState(defaultTo)
  const [filterFrom, setFilterFrom] = useState(defaultFrom)
  const [filterTo, setFilterTo] = useState(defaultTo)
  // «За всё время» — те же строки, только без ограничения по датам
  const [allTime, setAllTime] = useState(false)

  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [info, setInfo] = useState('')
  const [dragOver, setDragOver] = useState(false)
  const [editing, setEditing] = useState<ProductionRow | 'new' | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const loadReports = useCallback(async () => {
    setReports(await productionApi.reports())
  }, [])

  const loadRows = useCallback(async () => {
    setRows(
      await productionApi.rows(
        allTime
          ? { limit: 5000 }
          : { date_from: filterFrom || undefined, date_to: filterTo || undefined },
      ),
    )
  }, [filterFrom, filterTo, allTime])

  useEffect(() => {
    setLoading(true)
    Promise.all([loadReports(), loadRows()])
      .catch((err) => setError(message(err)))
      .finally(() => setLoading(false))
  }, [loadReports, loadRows])

  async function upload(file: File | undefined, replace = false): Promise<void> {
    if (!file) return
    if (!periodFrom || !periodTo) {
      setError('Укажите период, за который составлен рапорт')
      return
    }
    setError('')
    setInfo('')
    setBusy(true)
    try {
      const result = await productionApi.upload(file, periodFrom, periodTo, replace)
      setSummary(result.summary)
      setInfo(
        `Рапорт «${result.report.original_name}» загружен: ${formatNumber(
          result.summary.rows,
        )} строк, план ${formatNumber(result.summary.plan_total)}, факт ${formatNumber(
          result.summary.fact_total,
        )}.`,
      )
      await Promise.all([loadReports(), loadRows()])
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        if (window.confirm(`${err.message}. Заменить его данными из нового файла?`)) {
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

  async function removeReport(report: ProductionReport): Promise<void> {
    if (!window.confirm(`Удалить рапорт «${report.original_name}» и все его строки?`)) return
    setBusy(true)
    try {
      await productionApi.removeReport(report.id)
      await Promise.all([loadReports(), loadRows()])
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }

  async function removeRow(row: ProductionRow): Promise<void> {
    if (!window.confirm(`Удалить строку «${row.product}»?`)) return
    try {
      await productionApi.removeRow(row.id)
      await loadRows()
    } catch (err) {
      setError(message(err))
    }
  }

  const totals = rows.reduce(
    (acc, row) => ({
      plan: acc.plan + (row.plan_qty || 0),
      fact: acc.fact + (row.fact_qty || 0),
    }),
    { plan: 0, fact: 0 },
  )

  return (
    <>
      <Alert onClose={() => setError('')}>{error}</Alert>
      <Alert kind="success" onClose={() => setInfo('')}>
        {info}
      </Alert>

      <Card
        title="Загрузка рапорта"
        subtitle="«Справка о выполнении плана производства», файл Excel. Периода в файле нет — укажите его здесь"
      >
        <div className="period-row">
          <label className="field inline">
            <span>Период с</span>
            <input
              type="date"
              value={periodFrom}
              onChange={(event) => setPeriodFrom(event.target.value)}
            />
          </label>
          <label className="field inline">
            <span>по</span>
            <input
              type="date"
              value={periodTo}
              onChange={(event) => setPeriodTo(event.target.value)}
            />
          </label>
          <span className="sub muted">по умолчанию — текущий месяц</span>
        </div>

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
            accept=".xlsx,.xlsm"
            hidden
            onChange={(event) => void upload(event.target.files?.[0])}
          />
          <strong>{busy ? 'Обрабатываем файл…' : 'Перетащите рапорт сюда'}</strong>
          <span>или нажмите, чтобы выбрать файл на диске</span>
        </div>

        {summary && (
          <div className="import-report">
            <div>
              Строк номенклатуры: <b>{formatNumber(summary.rows)}</b> · изделий заведено{' '}
              <b>{summary.products_created}</b> · найдено в справочнике{' '}
              <b>{summary.products_matched}</b>
            </div>
            <div>
              План <b>{formatNumber(summary.plan_total)}</b> · факт{' '}
              <b>{formatNumber(summary.fact_total)}</b> ·{' '}
              {percent(summary.fact_total, summary.plan_total)}
            </div>
            {summary.shops.length > 0 && (
              <div className="muted">Цеха в файле: {summary.shops.join('; ')}</div>
            )}
            {summary.checks.map((check) => (
              <div key={check} className={check.includes('разошёлся') ? 'sub error' : 'sub'}>
                {check}
              </div>
            ))}
            {summary.warnings.length > 0 && (
              <div className="sub error">Пропущено: {summary.warnings.join('; ')}</div>
            )}
          </div>
        )}
      </Card>

      <Card title="Загруженные рапорты" className="wide">
        {loading ? (
          <Loader />
        ) : reports.length === 0 ? (
          <Empty>Рапорты ещё не загружались</Empty>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Период</th>
                  <th>Файл</th>
                  <th className="num">Строк</th>
                  <th className="num">План</th>
                  <th className="num">Факт</th>
                  <th className="num">Выполнение</th>
                  <th className="num">Размер</th>
                  <th>Загрузил</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {reports.map((report) => (
                  <tr key={report.id}>
                    <td>
                      {formatDate(report.period_from)} — {formatDate(report.period_to)}
                    </td>
                    <td className="ellipsis" title={report.original_name}>
                      {report.original_name}
                    </td>
                    <td className="num">{formatNumber(report.rows_count)}</td>
                    <td className="num">{formatNumber(report.plan_total)}</td>
                    <td className="num">{formatNumber(report.fact_total)}</td>
                    <td className="num">{percent(report.fact_total, report.plan_total)}</td>
                    <td className="num muted">{formatSize(report.size_bytes)}</td>
                    <td className="muted">
                      {report.uploaded_by || '—'}
                      <div className="sub">{formatDateTime(report.uploaded_at)}</div>
                    </td>
                    <td className="row-actions">
                      <button
                        type="button"
                        className="btn link"
                        onClick={() =>
                          void productionApi.download(report.id, report.original_name)
                        }
                      >
                        скачать
                      </button>
                      <button
                        type="button"
                        className="btn link danger"
                        onClick={() => void removeReport(report)}
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

      <Card
        title={allTime ? 'План и факт за всё время' : 'План и факт за период'}
        subtitle="выполнение = факт ÷ план по каждой строке"
        className="wide"
        actions={
          <button type="button" className="btn primary" onClick={() => setEditing('new')}>
            Добавить вручную
          </button>
        }
      >
        <div className="period-row">
          <div className="segmented">
            <button
              type="button"
              className={allTime ? '' : 'active'}
              onClick={() => setAllTime(false)}
            >
              За период
            </button>
            <button
              type="button"
              className={allTime ? 'active' : ''}
              onClick={() => setAllTime(true)}
            >
              За всё время
            </button>
          </div>

          {!allTime && (
            <>
              <label className="field inline">
                <span>С</span>
                <input
                  type="date"
                  value={filterFrom}
                  onChange={(event) => setFilterFrom(event.target.value)}
                />
              </label>
              <label className="field inline">
                <span>по</span>
                <input
                  type="date"
                  value={filterTo}
                  onChange={(event) => setFilterTo(event.target.value)}
                />
              </label>
            </>
          )}

          <span className="sub muted">
            {allTime
              ? 'показаны все строки плана и факта, какие есть в базе'
              : 'показаны строки, период которых пересекается с выбранным'}
          </span>
        </div>

        {rows.length === 0 ? (
          <Empty>{allTime ? 'Плана и факта пока нет' : 'За этот период данных нет'}</Empty>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Изделие</th>
                  <th>Тип корпуса</th>
                  <th>Цех</th>
                  <th>Станок</th>
                  <th>Период</th>
                  <th className="num">План</th>
                  <th className="num">Факт</th>
                  <th className="num">Выполнение</th>
                  <th className="num">Возврат</th>
                  <th>Источник</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={row.id}>
                    <td className="ellipsis" title={row.product}>
                      {row.product}
                    </td>
                    <td className="muted ellipsis">{row.body_type || '—'}</td>
                    <td className="muted ellipsis">{row.workshop || '—'}</td>
                    <td className="muted mono">{row.machine_key || '—'}</td>
                    <td className="muted">
                      {formatDate(row.period_from)} — {formatDate(row.period_to)}
                    </td>
                    <td className="num">
                      {row.plan_qty === null ? '—' : formatNumber(row.plan_qty)}
                    </td>
                    <td className="num">
                      {row.fact_qty === null ? '—' : formatNumber(row.fact_qty)}
                    </td>
                    <td className="num">
                      {row.percent === null ? '—' : `${row.percent.toFixed(1)} %`}
                    </td>
                    <td className="num muted">
                      {row.return_qty ? formatNumber(row.return_qty) : '—'}
                    </td>
                    <td>
                      <span className={`status status-${row.source === 'manual' ? 'ok' : 'pending'}`}>
                        {row.source === 'manual' ? 'вручную' : 'рапорт'}
                      </span>
                    </td>
                    <td className="row-actions">
                      <button type="button" className="btn link" onClick={() => setEditing(row)}>
                        править
                      </button>
                      <button
                        type="button"
                        className="btn link danger"
                        onClick={() => void removeRow(row)}
                      >
                        удалить
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
              <tfoot>
                <tr>
                  <td colSpan={5}>Итого строк: {formatNumber(rows.length)}</td>
                  <td className="num">{formatNumber(totals.plan)}</td>
                  <td className="num">{formatNumber(totals.fact)}</td>
                  <td className="num">{percent(totals.fact, totals.plan)}</td>
                  <td colSpan={3} />
                </tr>
              </tfoot>
            </table>
          </div>
        )}
      </Card>

      {editing && (
        <RowDialog
          row={editing === 'new' ? null : editing}
          defaultFrom={filterFrom}
          defaultTo={filterTo}
          onCancel={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void loadRows()
          }}
          onError={(text) => setError(text)}
        />
      )}
    </>
  )
}
