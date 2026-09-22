/** Таблица исходных записей мониторинга — самый подробный уровень данных. */
import { Empty, Loader } from '@/components/ui'
import { formatDate, formatDuration, formatNumber } from '@/lib/format'
import type { RecordsPage } from '@/types/api'

export function RecordTable({ data }: { data: RecordsPage | null }) {
  if (!data) return <Loader />
  if (!data.items.length) return <Empty>Нет записей</Empty>

  return (
    <>
      <p className="hint">
        Показано {data.items.length} из {formatNumber(data.total)} записей. Полная выборка —
        кнопкой «Выгрузить CSV».
      </p>
      <div className="table-wrap tall">
        <table className="table">
          <thead>
            <tr>
              <th>Дата</th>
              <th>Станок</th>
              <th>Смена</th>
              <th>Состояние</th>
              <th>Группа</th>
              <th className="num">Время</th>
              <th className="num">Событий</th>
            </tr>
          </thead>
          <tbody>
            {data.items.map((row) => (
              <tr key={row.id}>
                <td>{formatDate(row.calc_date)}</td>
                <td>{row.machine}</td>
                <td className="num">{row.shift ?? '—'}</td>
                <td>
                  <span className="dot" style={{ background: row.color || '#cbd5f5' }} />
                  {row.param}
                </td>
                <td className="muted">{row.group}</td>
                <td className="num">{formatDuration(row.seconds)}</td>
                <td className="num">{row.events}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}
