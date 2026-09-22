/** Таблица состояний: на что уходило время — по убыванию доли. */
import { Bar, Empty } from '@/components/ui'
import { formatHours, formatNumber, formatPercent } from '@/lib/format'
import type { ParamShare } from '@/types/api'

export function ParamTable({ rows }: { rows: ParamShare[] }) {
  if (!rows?.length) return <Empty>Нет данных</Empty>

  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th>Состояние</th>
            <th>Группа</th>
            <th>Код</th>
            <th className="num">Время</th>
            <th className="num" title="время состояния ÷ всё учтённое время выборки">
              Доля
            </th>
            <th className="num">Событий</th>
            <th className="bar-col" />
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={`${row.code}-${row.name}`}>
              <td>
                <span className="dot" style={{ background: row.color || '#cbd5f5' }} />
                {row.name}
              </td>
              <td className="muted">{row.group}</td>
              <td className="muted">{row.code || '—'}</td>
              <td className="num">{formatHours(row.seconds)}</td>
              <td className="num">{formatPercent(row.share)}</td>
              <td className="num">{formatNumber(row.events)}</td>
              <td className="bar-col">
                <Bar value={row.share} color={row.color} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
