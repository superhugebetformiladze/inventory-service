/** Таблица со сводкой по каждому станку; название — ссылка на страницу станка. */
import { Link } from 'react-router-dom'

import { Bar, Empty } from '@/components/ui'
import { formatHours, formatPercent, utilizationColor } from '@/lib/format'
import { machineHref } from '@/lib/links'
import type { DataQuery, MachineRow } from '@/types/api'

interface MachineTableProps {
  rows: MachineRow[]
  params: DataQuery
}

export function MachineTable({ rows, params }: MachineTableProps) {
  if (!rows?.length) return <Empty>Нет данных</Empty>

  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th>Станок</th>
            <th className="num" title="работа этого станка ÷ его учтённое время за период">
              Загрузка
            </th>
            <th className="num">Работа</th>
            <th className="num">Простой</th>
            <th className="num">Выключен</th>
            <th className="num">Всего</th>
            <th className="bar-col" />
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.key}>
              <td>
                <Link className="machine-link" to={machineHref(row.key, params)}>
                  {row.name}
                </Link>
              </td>
              <td className="num strong">{formatPercent(row.utilization)}</td>
              <td className="num">{formatHours(row.work_seconds)}</td>
              <td className="num">{formatHours(row.downtime_seconds)}</td>
              <td className="num muted">{formatHours(row.off_seconds)}</td>
              <td className="num">{formatHours(row.total_seconds)}</td>
              <td className="bar-col">
                <Bar value={row.utilization} color={utilizationColor(row.utilization)} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
