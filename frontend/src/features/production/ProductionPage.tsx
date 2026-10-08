/**
 * Страница «Продукция»: план, факт и выполнение по всей номенклатуре.
 *
 * Что здесь есть:
 *   * фильтры — период (или «за всё время»), цех, поиск по названию изделия;
 *   * показатели: план, сдано, выполнение плана, возврат;
 *   * две круговые диаграммы — как распределён выпуск между цехами и между изделиями;
 *   * таблица с правкой прямо в строке и кнопкой «Добавить строку»;
 *   * выгрузка всей выборки в Excel.
 *
 * Все числа на странице считаются из одних и тех же строк, которые видно в таблице,
 * поэтому диаграммы и итоги не могут разойтись с тем, что показано ниже.
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import { equipmentApi, productionApi } from '@/api'
import { ProductionPie } from '@/components/charts'
import { Alert, Card, Empty, Kpi, Loader } from '@/components/ui'
import { useAuth } from '@/auth/AuthContext'
import { formatNumber } from '@/lib/format'
import type { MachineBinding, ProductionRow } from '@/types/api'

import { RowDialog } from '../admin/production/RowDialog'
import { ProductionTable } from './ProductionTable'

/** Сколько строк тянем за раз. Месяц рапортов — это сотни строк, запас большой. */
const ROWS_LIMIT = 5000

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

/** Первое и последнее число текущего месяца в формате поля «дата». */
function monthBounds(base = new Date()): [string, string] {
  const iso = (value: Date) =>
    new Date(value.getTime() - value.getTimezoneOffset() * 60000).toISOString().slice(0, 10)
  return [
    iso(new Date(base.getFullYear(), base.getMonth(), 1)),
    iso(new Date(base.getFullYear(), base.getMonth() + 1, 0)),
  ]
}

export function ProductionPage() {
  const { isAdmin } = useAuth()
  const [defaultFrom, defaultTo] = monthBounds()

  // Фильтры живут в адресе — ссылку на нужный разрез можно отправить коллеге
  const [search, setSearch] = useSearchParams()
  const allTime = search.get('period') === 'all'
  const dateFrom = search.get('date_from') || defaultFrom
  const dateTo = search.get('date_to') || defaultTo
  const workshop = search.get('workshop') || ''
  const query = search.get('q') || ''

  const [rows, setRows] = useState<ProductionRow[]>([])
  const [machines, setMachines] = useState<MachineBinding[]>([])
  const [workshops, setWorkshops] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  const [exporting, setExporting] = useState(false)
  // Пока период не определён, запрос не шлём — иначе будет лишняя загрузка
  // текущего месяца, который тут же сменится периодом последнего рапорта
  const [periodReady, setPeriodReady] = useState(false)

  function patch(key: string, value: string): void {
    setSearch((current) => {
      const next = new URLSearchParams(current)
      if (value) next.set(key, value)
      else next.delete(key)
      return next
    })
  }

  const filters = useMemo(
    () => ({
      date_from: allTime ? undefined : dateFrom,
      date_to: allTime ? undefined : dateTo,
      workshop: workshop || undefined,
      search: query || undefined,
    }),
    [allTime, dateFrom, dateTo, workshop, query],
  )

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setRows(await productionApi.rows({ ...filters, limit: ROWS_LIMIT }))
    } catch (err) {
      setError(message(err))
    } finally {
      setLoading(false)
    }
  }, [filters])

  // Период по умолчанию — последний загруженный рапорт, а не текущий месяц:
  // иначе в начале месяца страница открывалась бы пустой, хотя данные есть.
  useEffect(() => {
    if (search.get('period') || search.get('date_from')) {
      setPeriodReady(true)
      return
    }
    productionApi
      .reports()
      .then((list) => {
        if (list.length) {
          setSearch(
            (current) => {
              const next = new URLSearchParams(current)
              next.set('date_from', list[0].period_from)
              next.set('date_to', list[0].period_to)
              return next
            },
            { replace: true },
          )
        }
      })
      .catch(() => undefined)
      .finally(() => setPeriodReady(true))
    // период подбирается один раз при открытии страницы
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (periodReady) void load()
  }, [load, periodReady])

  useEffect(() => {
    Promise.all([equipmentApi.machines(), productionApi.workshops()])
      .then(([bindings, shops]) => {
        setMachines(bindings)
        setWorkshops(shops)
      })
      .catch((err) => setError(message(err)))
  }, [])

  // --- всё, что показано на странице, считается из этих же строк ---
  const totals = useMemo(() => {
    const plan = rows.reduce((sum, row) => sum + (row.plan_qty || 0), 0)
    const fact = rows.reduce((sum, row) => sum + (row.fact_qty || 0), 0)
    const returned = rows.reduce((sum, row) => sum + (row.return_qty || 0), 0)
    return {
      plan,
      fact,
      returned,
      percent: plan ? (fact / plan) * 100 : null,
      products: new Set(rows.map((row) => row.product_id)).size,
    }
  }, [rows])

  const byWorkshop = useMemo(() => {
    const acc = new Map<string, number>()
    rows.forEach((row) => {
      const name = row.workshop || 'Без цеха'
      acc.set(name, (acc.get(name) ?? 0) + (row.fact_qty || 0))
    })
    return [...acc].map(([name, value]) => ({ name, value }))
  }, [rows])

  const byProduct = useMemo(() => {
    // Ключ — идентификатор изделия: одинаковые названия в разных цехах
    // это разные позиции номенклатуры, и складывать их вместе нельзя
    const acc = new Map<number, { name: string; value: number }>()
    rows.forEach((row) => {
      const label = row.body_type ? `${row.product} · ${row.body_type}` : row.product
      const found = acc.get(row.product_id)
      if (found) found.value += row.fact_qty || 0
      else acc.set(row.product_id, { name: label, value: row.fact_qty || 0 })
    })
    return [...acc.values()]
  }, [rows])

  async function exportExcel(): Promise<void> {
    setExporting(true)
    try {
      await productionApi.exportXlsx(filters)
    } catch (err) {
      setError(message(err))
    } finally {
      setExporting(false)
    }
  }

  const truncated = rows.length >= ROWS_LIMIT

  return (
    <div className="production-page">
      <Alert onClose={() => setError('')}>{error}</Alert>

      <div className="page-head">
        <div>
          <h1>Продукция</h1>
          <div className="page-head-meta">
            <span className="muted">
              план и факт выпуска; выполнение = сдано ÷ план
            </span>
          </div>
        </div>

        <div className="page-head-filters">
          <button
            type="button"
            className="btn ghost"
            disabled={exporting || !rows.length}
            onClick={() => void exportExcel()}
          >
            {exporting ? 'Готовим файл…' : 'Выгрузить в Excel'}
          </button>
          {isAdmin && (
            <button type="button" className="btn primary" onClick={() => setAdding(true)}>
              Добавить строку
            </button>
          )}
        </div>
      </div>

      <section className="filters">
        <div className="filters-row">
          <div className="segmented">
            <button
              type="button"
              className={allTime ? '' : 'active'}
              onClick={() => patch('period', '')}
            >
              За период
            </button>
            <button
              type="button"
              className={allTime ? 'active' : ''}
              onClick={() => patch('period', 'all')}
            >
              За всё время
            </button>
          </div>

          {allTime ? (
            <div className="field inline">
              <span>Период</span>
              <div className="chip">все загруженные данные</div>
            </div>
          ) : (
            <div className="field-group grow">
              <label className="field inline">
                <span>С</span>
                <input
                  type="date"
                  value={dateFrom}
                  onChange={(event) => patch('date_from', event.target.value)}
                />
              </label>
              <label className="field inline">
                <span>По</span>
                <input
                  type="date"
                  value={dateTo}
                  onChange={(event) => patch('date_to', event.target.value)}
                />
              </label>
            </div>
          )}

          <label className="field inline">
            <span>Цех</span>
            <select value={workshop} onChange={(event) => patch('workshop', event.target.value)}>
              <option value="">Все цеха</option>
              {workshops.map((item) => (
                <option key={item} value={item}>
                  {item}
                </option>
              ))}
            </select>
          </label>

          <label className="field inline grow">
            <span>Поиск по изделию</span>
            <input
              value={query}
              placeholder="название или тип корпуса"
              onChange={(event) => patch('q', event.target.value)}
            />
          </label>
        </div>

        <p className="hint">
          Показаны строки, период которых пересекается с выбранным. Строки из рапортов
          заведены по цехам, строки с указанным станком — введены вручную.
        </p>
      </section>

      {loading && <Loader />}

      {!loading && rows.length === 0 && (
        <Card title="План и факт">
          <Empty>
            За эту выборку данных нет. Загрузите рапорт в «Админка → Продукция» или
            добавьте строку вручную.
          </Empty>
        </Card>
      )}

      {!loading && rows.length > 0 && (
        <>
          <div className="kpi-row">
            <Kpi
              label="План"
              value={formatNumber(totals.plan)}
              hint={`по ${formatNumber(rows.length)} строкам выборки, шт.`}
            />
            <Kpi
              label="Сдано на склад"
              value={formatNumber(totals.fact)}
              hint="сумма фактического выпуска, шт."
              tone="good"
            />
            <Kpi
              label="Выполнение плана"
              value={totals.percent === null ? '—' : `${totals.percent.toFixed(1)} %`}
              hint="сдано ÷ план по всей выборке"
              tone="accent"
            />
            <Kpi
              label="Возврат"
              value={formatNumber(totals.returned)}
              hint="возвращено со склада ПДО, шт."
              tone={totals.returned > 0 ? 'warn' : 'default'}
            />
            <Kpi
              label="Номенклатура"
              value={formatNumber(totals.products)}
              hint="различных изделий в выборке"
            />
          </div>

          <div className="grid-2">
            <Card
              title="Выпуск по цехам"
              subtitle="доля цеха = его факт ÷ весь факт выборки; пять крупнейших, остальные — «Прочие»"
            >
              <ProductionPie items={byWorkshop} />
            </Card>

            <Card
              title="Выпуск по изделиям"
              subtitle="доля изделия = его факт ÷ весь факт выборки; пять крупнейших, остальные — «Прочие»"
            >
              <ProductionPie items={byProduct} />
            </Card>
          </div>

          <Card
            title="План и факт по строкам"
            subtitle={
              isAdmin
                ? 'нажмите «править», чтобы изменить план, факт, возврат, станок или примечание прямо в строке'
                : 'выполнение = сдано ÷ план по каждой строке'
            }
            className="wide"
          >
            {truncated && (
              <p className="sub error">
                Показаны первые {formatNumber(ROWS_LIMIT)} строк — сузьте период или
                выберите цех, чтобы увидеть всё.
              </p>
            )}
            <ProductionTable
              rows={rows}
              machines={machines}
              canEdit={isAdmin}
              onChanged={() => void load()}
              onError={setError}
            />
          </Card>
        </>
      )}

      {adding && (
        <RowDialog
          row={null}
          defaultFrom={allTime ? defaultFrom : dateFrom}
          defaultTo={allTime ? defaultTo : dateTo}
          onCancel={() => setAdding(false)}
          onSaved={() => {
            setAdding(false)
            void load()
          }}
          onError={setError}
        />
      )}
    </div>
  )
}
