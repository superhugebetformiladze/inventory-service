/**
 * Страница одного станка.
 *
 * Показывает паспорт из карточки оборудования и показатели этого станка за период:
 * загрузку, место в парке, структуру времени, динамику по суткам и разбивку по дням.
 *
 * Фильтры хранятся прямо в адресе страницы, поэтому ссылку можно отправить коллеге —
 * он увидит ровно тот же разрез данных.
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'

import { dataApi, filesApi } from '@/api'
import { GroupBreakdown, TimelineChart } from '@/components/charts'
import { Formulas } from '@/components/Formulas'
import { ParamTable } from '@/components/tables'
import { Alert, Card, Empty, Kpi, Loader } from '@/components/ui'
import { formatDate, formatHours, formatPercent } from '@/lib/format'
import type { DataQuery, MachineDetail, SourceFile } from '@/types/api'

import { MachineDaysTable } from './MachineDaysTable'
import { MachinePassport } from './MachinePassport'

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function MachinePage() {
  const { machineKey = '' } = useParams<{ machineKey: string }>()
  const [search, setSearch] = useSearchParams()

  const [files, setFiles] = useState<SourceFile[]>([])
  const [detail, setDetail] = useState<MachineDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  // Фильтры живут в адресной строке: ссылка на станок открывается в том же разрезе.
  const scope = (search.get('scope') as 'file' | 'range') || 'file'
  const fileId = search.get('file_id') || ''
  const scheduleType = search.get('schedule_type') || '1'
  const shift = search.get('shift') || ''
  const dateFrom = search.get('date_from') || ''
  const dateTo = search.get('date_to') || ''

  useEffect(() => {
    filesApi
      .list()
      .then((rows) => {
        const ready = rows.filter((row) => row.status === 'ok')
        setFiles(ready)
        if (scope === 'file' && !fileId && ready.length) {
          setSearch(
            (current) => {
              const next = new URLSearchParams(current)
              next.set('file_id', String(ready[0].id))
              return next
            },
            { replace: true },
          )
        }
      })
      .catch((err) => setError(message(err)))
    // список файлов нужен один раз — дальше фильтры живут в адресной строке
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const params = useMemo<DataQuery>(() => {
    const base: DataQuery = {
      scope,
      schedule_type: Number(scheduleType),
      shift: shift || undefined,
    }
    if (scope === 'file') return { ...base, file_id: fileId ? Number(fileId) : undefined }
    return { ...base, date_from: dateFrom || undefined, date_to: dateTo || undefined }
  }, [scope, fileId, scheduleType, shift, dateFrom, dateTo])

  const load = useCallback(async () => {
    if (scope === 'file' && !fileId) return
    setLoading(true)
    setError('')
    try {
      setDetail(await dataApi.machineDetail(machineKey, params))
    } catch (err) {
      setError(message(err))
      setDetail(null)
    } finally {
      setLoading(false)
    }
  }, [machineKey, params, scope, fileId])

  useEffect(() => {
    void load()
  }, [load])

  function patch(key: string, value: string) {
    setSearch((current) => {
      const next = new URLSearchParams(current)
      if (value) next.set(key, value)
      else next.delete(key)
      return next
    })
  }

  const card = detail?.card
  const totals = detail?.totals
  const fleet = detail?.fleet

  return (
    <div className="machine-page">
      <Alert onClose={() => setError('')}>{error}</Alert>

      <div className="page-head">
        <div>
          <Link to={`/?${search.toString()}`} className="btn link back">
            ← ко всем станкам
          </Link>
          <h1>{detail?.title || machineKey}</h1>
          <div className="page-head-meta">
            <span className="mono">{machineKey}</span>
            {card?.workshop && <span className="chip">цех {card.workshop}</span>}
            {card?.model && <span className="chip">{card.model}</span>}
            {card?.cnc_system && <span className="chip">{card.cnc_system}</span>}
            {detail && !card && <span className="chip warn">карточка не заведена</span>}
            {card?.binding_kind === 'guess' && (
              <span
                className="chip warn"
                title="Код MDM общий для нескольких одинаковых станков — привязку стоит проверить"
              >
                привязка предположительная
              </span>
            )}
          </div>
        </div>

        <div className="page-head-filters">
          {scope === 'file' ? (
            <label className="field inline">
              <span>Выгрузка</span>
              <select value={fileId} onChange={(event) => patch('file_id', event.target.value)}>
                {files.map((file) => (
                  <option key={file.id} value={file.id}>
                    {formatDate(file.monitoring_from)} — {formatDate(file.monitoring_to)}
                  </option>
                ))}
              </select>
            </label>
          ) : (
            <label className="field inline">
              <span>Период</span>
              <div className="chip">
                {dateFrom ? formatDate(dateFrom) : '…'} — {dateTo ? formatDate(dateTo) : '…'}
              </div>
            </label>
          )}
          <label className="field inline">
            <span>Учёт времени</span>
            <select
              value={scheduleType}
              onChange={(event) => patch('schedule_type', event.target.value)}
            >
              <option value="1">По графику смен</option>
              <option value="2">Календарные сутки</option>
            </select>
          </label>
          <label className="field inline">
            <span>Смена</span>
            <select value={shift} onChange={(event) => patch('shift', event.target.value)}>
              <option value="">Все</option>
              <option value="1">1</option>
              <option value="2">2</option>
            </select>
          </label>
        </div>
      </div>

      {loading && <Loader />}

      {!loading && detail && totals && fleet && (
        <>
          <div className="kpi-row">
            <Kpi
              label="Коэффициент загрузки"
              value={formatPercent(totals.utilization)}
              hint={`работа ${formatHours(totals.work_seconds, 0)} ÷ учтённое время станка ${formatHours(
                totals.total_seconds,
                0,
              )}; по парку ${formatPercent(fleet.utilization)}`}
              tone="accent"
            />
            <Kpi
              label="Место в парке"
              value={fleet.position ? `${fleet.position} из ${fleet.machines}` : '—'}
              hint="место по коэффициенту загрузки среди станков выборки"
            />
            <Kpi
              label="Работа по программе"
              value={formatHours(totals.work_seconds, 0)}
              hint="состояние 0101 — числитель коэффициента загрузки"
              tone="good"
            />
            <Kpi
              label="Простои"
              value={formatHours(totals.downtime_seconds, 0)}
              hint={`${formatPercent(
                totals.total_seconds ? (totals.downtime_seconds / totals.total_seconds) * 100 : 0,
              )} от учтённого времени станка; выключен ${formatHours(totals.off_seconds, 0)}`}
              tone="warn"
            />
            <Kpi
              label="Суток без работы"
              value={`${fleet.idle_days} из ${totals.days}`}
              hint={
                fleet.best_day
                  ? `лучший день ${formatDate(fleet.best_day.date)} — ${formatPercent(
                      fleet.best_day.utilization,
                    )}`
                  : 'работы за период не было'
              }
            />
          </div>

          <div className="grid-2">
            <Card title="Паспорт станка" subtitle={card ? `инв. № ${card.inv_number}` : undefined}>
              <MachinePassport card={card ?? null} />
            </Card>

            <Card
              title="Структура времени"
              subtitle="доля группы = её время ÷ учтённое время станка за период"
            >
              {detail.by_group.length ? (
                <GroupBreakdown items={detail.by_group} totalSeconds={totals.total_seconds} />
              ) : (
                <Empty>Нет данных за период</Empty>
              )}
            </Card>
          </div>

          <Card
            title="Динамика по суткам"
            subtitle="за каждые сутки: часы по состояниям и загрузка = работа ÷ учтённое время станка за эти сутки"
          >
            {detail.by_day.length ? (
              <TimelineChart rows={detail.by_day} />
            ) : (
              <Empty>Нет данных за период</Empty>
            )}
          </Card>

          <Card
            title="Состояния"
            subtitle="доля = время состояния ÷ учтённое время станка за период"
          >
            <ParamTable rows={detail.by_param} />
          </Card>

          <Card
            title="По суткам"
            subtitle="загрузка за сутки = работа ÷ учтённое время станка за эти сутки"
          >
            <MachineDaysTable rows={detail.by_day} />
          </Card>

          <Formulas className="" />
        </>
      )}
    </div>
  )
}
