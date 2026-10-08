/**
 * Сводка выпуска продукции за тот же период, что и показатели загрузки.
 *
 * План и факт заводятся отдельно от мониторинга — рапортом за месяц или вручную,
 * поэтому карточка сама запрашивает их за границы выбранного периода. Если ничего
 * не загружено, блок не показывается вовсе, чтобы не занимать место пустой таблицей.
 */
import { useEffect, useState } from 'react'

import { productionApi } from '@/api'
import { Bar, Card, Empty } from '@/components/ui'
import { formatNumber } from '@/lib/format'
import type { ProductionSummary } from '@/types/api'

interface ProductionCardProps {
  /** Границы периода в виде «ГГГГ-ММ-ДД». */
  dateFrom?: string
  dateTo?: string
}

function percentText(value: number | null): string {
  return value === null ? '—' : `${value.toFixed(1)} %`
}

export function ProductionCard({ dateFrom, dateTo }: ProductionCardProps) {
  const [data, setData] = useState<ProductionSummary | null>(null)

  useEffect(() => {
    productionApi
      .summary({ date_from: dateFrom, date_to: dateTo })
      .then(setData)
      .catch(() => setData(null))
  }, [dateFrom, dateTo])

  // Ни одного изделия в справочнике — раздел ещё не используют, не мешаем ему на экране
  if (!data || data.products_total === 0) return null

  const { totals, by_workshop: byWorkshop, by_product: byProduct } = data

  return (
    <Card
      title="План и факт производства"
      subtitle="выполнение = сделано ÷ план за период; данные из рапортов и ручного ввода, к загрузке станков не привязаны"
      className="wide"
    >
      {totals.rows === 0 ? (
        <Empty>За этот период плана и факта нет</Empty>
      ) : (
        <>
          <div className="kpi-row inline">
            <div className="kpi kpi-default">
              <div className="kpi-label">План</div>
              <div className="kpi-value">{formatNumber(totals.plan_qty)}</div>
              <div className="kpi-hint">строк плана и факта: {totals.rows}</div>
            </div>
            <div className="kpi kpi-good">
              <div className="kpi-label">Сдано</div>
              <div className="kpi-value">{formatNumber(totals.fact_qty)}</div>
              <div className="kpi-hint">возврат {formatNumber(totals.return_qty)}</div>
            </div>
            <div className="kpi kpi-accent">
              <div className="kpi-label">Выполнение плана</div>
              <div className="kpi-value">{percentText(totals.percent)}</div>
              <div className="kpi-hint">сдано ÷ план по всей номенклатуре</div>
            </div>
            <div className="kpi kpi-default">
              <div className="kpi-label">Номенклатура</div>
              <div className="kpi-value">{data.products_total}</div>
              <div className="kpi-hint">закреплено за станками: {data.linked_machines}</div>
            </div>
          </div>

          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Цех</th>
                  <th className="num">План</th>
                  <th className="num">Сдано</th>
                  <th className="num">Выполнение</th>
                  <th className="bar-col">Доля выполнения</th>
                </tr>
              </thead>
              <tbody>
                {byWorkshop.map((shop) => (
                  <tr key={shop.workshop}>
                    <td className="ellipsis" title={shop.workshop}>
                      {shop.workshop}
                    </td>
                    <td className="num">{formatNumber(shop.plan_qty)}</td>
                    <td className="num">{formatNumber(shop.fact_qty)}</td>
                    <td className="num">{percentText(shop.percent)}</td>
                    <td className="bar-col">
                      <Bar
                        value={shop.percent ?? 0}
                        color={(shop.percent ?? 0) >= 100 ? 'var(--good)' : 'var(--warn)'}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {byProduct.length > 0 && (
            <p className="sub muted">
              Крупнейшая позиция по плану: {byProduct[0].name} — план{' '}
              {formatNumber(byProduct[0].plan_qty)}, сдано {formatNumber(byProduct[0].fact_qty)} (
              {percentText(byProduct[0].percent)}).
            </p>
          )}
        </>
      )}
    </Card>
  )
}
