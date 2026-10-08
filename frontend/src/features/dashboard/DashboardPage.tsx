/**
 * Главный экран — аналитика по всему парку станков.
 *
 * Сверху панель фильтров, под ней пять показателей, дальше графики и таблицы.
 * Сами данные приходят из хука useDashboardData: он обращается к серверу и хранит
 * состояние, а этот файл отвечает только за то, как всё разложено на странице.
 */
import { useEffect, useState } from 'react'

import { GroupBreakdown, Heatmap, MachinesChart, TimelineChart } from '@/components/charts'
import { Formulas } from '@/components/Formulas'
import { MachineTable, ParamTable, RecordTable } from '@/components/tables'
import { Alert, Card, Empty, Kpi, Loader } from '@/components/ui'
import { formatDate, formatHours, formatPercent } from '@/lib/format'

import { FilterBar } from './FilterBar'
import { ProductionCard } from './ProductionCard'
import { useDashboardData } from './useDashboardData'

type Tab = 'params' | 'machines' | 'records'

const TABS: [Tab, string][] = [
  ['params', 'Состояния'],
  ['machines', 'Станки'],
  ['records', 'Записи'],
]

export function DashboardPage() {
  const state = useDashboardData()
  const [tab, setTab] = useState<Tab>('params')

  const { files, machines, availableShifts, filters, params, overview, loading, error, loadRecords } =
    state
  const totals = overview?.totals

  // Записи тянем только когда открыли вкладку: выборка тяжёлая и нужна не всем.
  useEffect(() => {
    if (tab === 'records') loadRecords()
  }, [tab, loadRecords])

  if (!files.length && !loading) {
    return (
      <Empty>
        Нет загруженных файлов. Попросите администратора загрузить XML-выгрузку в разделе
        «Админка».
      </Empty>
    )
  }

  return (
    <div className="dashboard">
      <Alert onClose={() => state.setError('')}>{error}</Alert>

      <FilterBar
        files={files}
        machines={machines}
        availableShifts={availableShifts}
        filters={filters}
        params={params}
      />

      {loading && <Loader />}

      {!loading && overview && totals && (
        <>
          <div className="kpi-row">
            <Kpi
              label="Коэффициент загрузки"
              value={formatPercent(totals.utilization)}
              hint={`работа ${formatHours(totals.work_seconds, 0)} ÷ всё учтённое время ${formatHours(
                totals.total_seconds,
                0,
              )}`}
              tone="accent"
            />
            <Kpi
              label="Загрузка при включённом станке"
              value={formatPercent(totals.utilization_powered)}
              hint={`работа ${formatHours(totals.work_seconds, 0)} ÷ ${formatHours(
                totals.powered_seconds,
                0,
              )} (без выключенного ${formatHours(totals.off_seconds, 0)})`}
            />
            <Kpi
              label="Работа по программе"
              value={formatHours(totals.work_seconds, 0)}
              hint="состояние 0101, из него считается коэффициент загрузки"
              tone="good"
            />
            <Kpi
              label="Простои"
              value={formatHours(totals.downtime_seconds, 0)}
              hint={`${formatPercent(
                totals.total_seconds ? (totals.downtime_seconds / totals.total_seconds) * 100 : 0,
              )} от всего учтённого времени; выключено ${formatHours(totals.off_seconds, 0)}`}
              tone="warn"
            />
            <Kpi
              label="Станков / суток"
              value={`${totals.machines} / ${totals.days}`}
              hint={`${formatDate(totals.date_min)} — ${formatDate(totals.date_max)}`}
            />
          </div>

          <div className="grid-2">
            <Card
              title="Структура времени по группам"
              subtitle="доля группы = её время ÷ всё учтённое время за период; сумма долей — 100 %"
            >
              {overview.by_group.length ? (
                <GroupBreakdown items={overview.by_group} totalSeconds={totals.total_seconds} />
              ) : (
                <Empty>Нет данных за выбранный период</Empty>
              )}
            </Card>

            <Card
              title="Динамика по суткам"
              subtitle="за каждые сутки: часы по состояниям и загрузка = работа ÷ учтённое время этих суток (по всем выбранным станкам); в выходные смена целиком «выключено»"
            >
              {state.timeline.length ? (
                <TimelineChart rows={state.timeline} />
              ) : (
                <Empty>Нет данных за выбранный период</Empty>
              )}
            </Card>
          </div>

          <Card
            title="Станки"
            subtitle="структура времени по каждому станку в часах; клик по названию открывает страницу станка"
            className="wide"
          >
            {state.machineRows.length ? (
              <MachinesChart
                rows={state.machineRows}
                groups={overview.by_group}
                params={params}
              />
            ) : (
              <Empty>Нет данных</Empty>
            )}
          </Card>

          <ProductionCard
            dateFrom={
              state.filters.scope === 'all' || !state.timeline.length
                ? undefined
                : state.timeline[0].date.slice(0, 10)
            }
            dateTo={
              state.filters.scope === 'all' || !state.timeline.length
                ? undefined
                : state.timeline[state.timeline.length - 1].date.slice(0, 10)
            }
          />

          {state.heatmap && state.heatmap.cells.length > 0 && (
            <Card
              title="Загрузка по суткам"
              subtitle="в ячейке — загрузка станка за сутки: его работа ÷ его учтённое время за эти сутки"
              className="wide"
            >
              <Heatmap data={state.heatmap} params={params} />
            </Card>
          )}

          <Card
            className="wide"
            title="Детализация"
            actions={
              <div className="tabs">
                {TABS.map(([key, label]) => (
                  <button
                    key={key}
                    type="button"
                    className={tab === key ? 'active' : ''}
                    onClick={() => setTab(key)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            }
          >
            {tab === 'params' && <ParamTable rows={overview.by_param} />}
            {tab === 'machines' && <MachineTable rows={state.machineRows} params={params} />}
            {tab === 'records' && <RecordTable data={state.records} />}
          </Card>

          <Formulas />
        </>
      )}
    </div>
  )
}
