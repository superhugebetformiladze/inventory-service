/**
 * Блок «Продукция» на странице станка: что станок делает и как выполнен план.
 *
 * Важная оговорка, которую видно и в интерфейсе: рапорт составляется по цехам, а не
 * по станкам. Поэтому план и факт здесь — это показатели закреплённых за станком
 * изделий. Если то же изделие делают ещё на других станках, цифра общая на всех —
 * такие строки помечены. Точные цифры по станку появляются, когда строку вводят
 * вручную с указанием станка.
 */
import { Bar, Empty } from '@/components/ui'
import { formatNumber } from '@/lib/format'
import type { MachineProduction } from '@/types/api'

interface MachineProductsProps {
  data: MachineProduction | null
  loading?: boolean
}

export function MachineProducts({ data, loading = false }: MachineProductsProps) {
  if (loading) return <div className="sub muted">Загрузка…</div>

  if (!data || data.linked === 0) {
    return (
      <Empty>
        За станком не закреплено ни одного изделия. Это делается в админке:
        «Продукция → Номенклатура и станки».
      </Empty>
    )
  }

  if (data.products.length === 0) {
    return <Empty>Изделия закреплены, но плана и факта за этот период нет</Empty>
  }

  return (
    <>
      <div className="table-wrap">
        <table className="table">
          <thead>
            <tr>
              <th>Изделие</th>
              <th>Тип корпуса</th>
              <th className="num">План</th>
              <th className="num">Факт</th>
              <th className="num">Выполнение</th>
              <th className="bar-col">Доля выполнения</th>
            </tr>
          </thead>
          <tbody>
            {data.products.map((item) => (
              <tr key={item.product_id}>
                <td className="ellipsis" title={item.name}>
                  {item.name}
                  {(item.machines ?? 1) > 1 && (
                    <span
                      className="chip warn small"
                      title="Это изделие делают несколько станков — план и факт общие на всех"
                    >
                      на {item.machines} станках
                    </span>
                  )}
                  {item.manual && (
                    <span className="chip small" title="Есть строки, введённые вручную">
                      вручную
                    </span>
                  )}
                </td>
                <td className="muted ellipsis">{item.body_type || '—'}</td>
                <td className="num">{item.plan_qty ? formatNumber(item.plan_qty) : '—'}</td>
                <td className="num">{formatNumber(item.fact_qty)}</td>
                <td className="num">
                  {item.percent === null ? '—' : `${item.percent.toFixed(1)} %`}
                </td>
                <td className="bar-col">
                  <Bar
                    value={item.percent ?? 0}
                    color={
                      item.percent === null
                        ? 'var(--muted)'
                        : item.percent >= 100
                          ? 'var(--good)'
                          : 'var(--warn)'
                    }
                  />
                </td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr>
              <td colSpan={2}>Итого по закреплённой продукции</td>
              <td className="num">{formatNumber(data.totals.plan_qty)}</td>
              <td className="num">{formatNumber(data.totals.fact_qty)}</td>
              <td className="num">
                {data.totals.percent === null ? '—' : `${data.totals.percent.toFixed(1)} %`}
              </td>
              <td />
            </tr>
          </tfoot>
        </table>
      </div>

      {data.shared && (
        <p className="sub muted">
          Часть изделий выпускается не только на этом станке — их план и факт общие на все
          станки, за которыми изделие закреплено. Чтобы вести выпуск именно по станку,
          добавьте строку вручную с указанием станка.
        </p>
      )}
    </>
  )
}
