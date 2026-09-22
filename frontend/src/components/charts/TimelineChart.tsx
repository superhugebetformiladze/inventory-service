/**
 * График «Динамика по суткам».
 *
 * Закрашенные области — часы по состояниям (работа, простой, выключено), они
 * складываются друг на друга и вместе дают всё учтённое время за сутки. Чёрная линия
 * поверх — коэффициент загрузки в процентах, у неё своя шкала справа.
 */
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { formatDate, hours } from '@/lib/format'
import type { DayRow } from '@/types/api'

interface TimelineChartProps {
  rows: DayRow[]
  height?: number
}

export function TimelineChart({ rows, height = 320 }: TimelineChartProps) {
  const data = rows.map((row) => ({
    date: formatDate(row.date).slice(0, 5),
    utilization: row.utilization,
    work: hours(row.work_seconds),
    downtime: hours(row.downtime_seconds),
    off: hours(row.off_seconds),
  }))

  return (
    <ResponsiveContainer width="100%" height={height}>
      <ComposedChart data={data}>
        <CartesianGrid strokeDasharray="3 3" stroke="#eef2f7" />
        <XAxis dataKey="date" tick={{ fontSize: 12 }} />
        <YAxis yAxisId="left" tick={{ fontSize: 12 }} unit=" ч" />
        <YAxis
          yAxisId="right"
          orientation="right"
          tick={{ fontSize: 12 }}
          unit=" %"
          domain={[0, 100]}
        />
        <Tooltip
          formatter={(value, name) =>
            name === 'Загрузка, %'
              ? [`${value} %`, name]
              : [`${Number(value).toFixed(1)} ч`, name]
          }
          contentStyle={{ borderRadius: 10, border: '1px solid #e2e8f0' }}
        />
        <Legend formatter={(value) => <span className="legend-text">{value}</span>} />
        <Area
          yAxisId="left"
          type="monotone"
          dataKey="work"
          name="Работа"
          stackId="1"
          stroke="#16a34a"
          fill="#bbf7d0"
        />
        <Area
          yAxisId="left"
          type="monotone"
          dataKey="downtime"
          name="Простой"
          stackId="1"
          stroke="#f97316"
          fill="#fed7aa"
        />
        <Area
          yAxisId="left"
          type="monotone"
          dataKey="off"
          name="Выключено"
          stackId="1"
          stroke="#94a3b8"
          fill="#e2e8f0"
        />
        <Line
          yAxisId="right"
          type="monotone"
          dataKey="utilization"
          name="Загрузка, %"
          stroke="#0f172a"
          strokeWidth={2}
          dot={false}
        />
      </ComposedChart>
    </ResponsiveContainer>
  )
}
