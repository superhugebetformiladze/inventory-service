/**
 * График «Станки»: горизонтальная полоса на каждый станок.
 *
 * Полоса разбита на части по группам состояний, длина части — часы. Названия станков
 * слева сделаны ссылками на страницу станка: библиотека графиков сообщает нам номер
 * строки, по нему берём ключ станка, поэтому одинаковые названия не путаются.
 */
import { useNavigate } from 'react-router-dom'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { hours, shortenLabel } from '@/lib/format'
import { groupColor, groupShort, sortGroups } from '@/lib/groups'
import { machineHref } from '@/lib/links'
import { useNarrowScreen } from '@/lib/useNarrowScreen'
import type { DataQuery, GroupShare, MachineRow } from '@/types/api'

interface MachinesChartProps {
  rows: MachineRow[]
  groups: GroupShare[]
  params: DataQuery
}

interface ChartRow {
  name: string
  key: string
  utilization: number
  [group: string]: string | number
}

interface TickProps {
  x?: number
  y?: number
  index?: number
  payload?: { value: string }
  rows: ChartRow[]
  params: DataQuery
  narrow: boolean
  onOpen: (href: string) => void
}

/**
 * Подпись оси — ссылка на страницу станка. Recharts передаёт в tick координаты
 * и индекс точки: по индексу берём ключ станка, поэтому совпадение по названию не нужно.
 */
function MachineTick({ x, y, payload, index, rows, params, narrow, onOpen }: TickProps) {
  const row = index === undefined ? undefined : rows[index]
  if (!row || !payload) return null

  const label = narrow ? shortenLabel(payload.value, 12) : payload.value
  const href = machineHref(row.key, params)

  return (
    <a
      href={href}
      onClick={(event) => {
        if (event.metaKey || event.ctrlKey || event.shiftKey) return
        event.preventDefault()
        onOpen(href)
      }}
    >
      <text x={x} y={y} dy={4} textAnchor="end" className="chart-link" fontSize={narrow ? 9 : 11}>
        <title>{payload.value}</title>
        {label}
      </text>
    </a>
  )
}

export function MachinesChart({ rows, groups, params }: MachinesChartProps) {
  const narrow = useNarrowScreen()
  const navigate = useNavigate()

  const ordered = sortGroups(groups)
  const data: ChartRow[] = rows.map((row) => {
    const item: ChartRow = { name: row.name, key: row.key, utilization: row.utilization }
    ordered.forEach((group) => {
      item[group.group] = hours(row.groups?.[group.group] || 0)
    })
    return item
  })

  return (
    <ResponsiveContainer width="100%" height={Math.max(300, data.length * (narrow ? 20 : 26))}>
      <BarChart data={data} layout="vertical" margin={{ left: narrow ? 0 : 16, right: 8 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="#eef2f7" />
        <XAxis type="number" tick={{ fontSize: 11 }} unit=" ч" />
        <YAxis
          type="category"
          dataKey="name"
          width={narrow ? 100 : 190}
          interval={0}
          tick={
            <MachineTick
              rows={data}
              params={params}
              narrow={narrow}
              onOpen={(href) => navigate(href)}
            />
          }
        />
        <Tooltip
          formatter={(value, name) => [`${Number(value).toFixed(1)} ч`, name]}
          contentStyle={{ borderRadius: 10, border: '1px solid #e2e8f0' }}
        />
        <Legend formatter={(value) => <span className="legend-text">{value}</span>} />
        {ordered.map((group) => (
          <Bar
            key={group.group}
            dataKey={group.group}
            name={groupShort(group.group, group.ext_id)}
            stackId="time"
            fill={groupColor(group.group, group.ext_id)}
          />
        ))}
      </BarChart>
    </ResponsiveContainer>
  )
}
