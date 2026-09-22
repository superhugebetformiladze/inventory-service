/** Таблица «по суткам» для одного станка: сколько работал и простаивал каждый день. */
import { formatDate, formatHours, formatPercent, utilizationColor } from '@/lib/format'
import type { DayRow } from '@/types/api'

export function MachineDaysTable({ rows }: { rows: DayRow[] }) {
  return (
    <div className="table-wrap tall">
      <table className="table">
        <thead>
          <tr>
            <th>Дата</th>
            <th className="num" title="работа ÷ учтённое время станка за эти сутки">
              Загрузка
            </th>
            <th className="num">Работа</th>
            <th className="num">Простой</th>
            <th className="num">Выключен</th>
            <th className="bar-col" />
          </tr>
        </thead>
        <tbody>
          {rows.map((day) => (
            <tr key={day.date}>
              <td>{formatDate(day.date)}</td>
              <td className="num strong">{formatPercent(day.utilization)}</td>
              <td className="num">{formatHours(day.work_seconds)}</td>
              <td className="num">{formatHours(day.downtime_seconds)}</td>
              <td className="num muted">{formatHours(day.off_seconds)}</td>
              <td className="bar-col">
                <div className="mini-bar">
                  <span
                    style={{
                      width: `${Math.min(day.utilization, 100)}%`,
                      background: utilizationColor(day.utilization),
                    }}
                  />
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
