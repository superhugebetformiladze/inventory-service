/**
 * Тепловая карта: строки — станки, столбцы — сутки.
 *
 * Цвет ячейки показывает загрузку станка за эти сутки: чем зеленее, тем больше он
 * работал. Серые ячейки — данных нет. Так с одного взгляда видно, какой станок простаивал.
 */
import { useMemo } from 'react'
import { Link } from 'react-router-dom'

import { formatDuration, utilizationColor } from '@/lib/format'
import { machineHref } from '@/lib/links'
import type { DataQuery, Heatmap as HeatmapData, HeatmapCell } from '@/types/api'

interface HeatmapProps {
  data: HeatmapData
  params: DataQuery
}

export function Heatmap({ data, params }: HeatmapProps) {
  const cells = useMemo(() => {
    const map = new Map<string, HeatmapCell>()
    data.cells.forEach((cell) => map.set(`${cell.machine}|${cell.date}`, cell))
    return map
  }, [data])

  return (
    <div className="heatmap-scroll">
      <table className="heatmap">
        <thead>
          <tr>
            <th className="sticky">Станок</th>
            {data.dates.map((date) => (
              <th key={date}>
                {date.slice(8, 10)}.{date.slice(5, 7)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {data.machines.map((machine) => (
            <tr key={machine.key}>
              <td className="sticky">
                <Link className="machine-link" to={machineHref(machine.key, params)}>
                  {machine.name}
                </Link>
              </td>
              {data.dates.map((date) => {
                const cell = cells.get(`${machine.key}|${date}`)
                return (
                  <td
                    key={date}
                    className="heat-cell"
                    style={{ background: cell ? utilizationColor(cell.utilization) : '#f8fafc' }}
                    title={
                      cell
                        ? `${machine.name} · ${date}\nЗагрузка ${cell.utilization} %\nРабота ${formatDuration(
                            cell.work_seconds,
                          )} из ${formatDuration(cell.total_seconds)}`
                        : 'нет данных'
                    }
                  >
                    {cell ? Math.round(cell.utilization) : ''}
                  </td>
                )
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
