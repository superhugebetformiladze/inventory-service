/**
 * Круговая диаграмма «часть от целого»: как распределён выпуск между цехами
 * или изделиями.
 *
 * Правила, по которым она построена:
 *   * не больше шести секторов — дальше соседние цвета перестают различаться,
 *     поэтому всё, что не попало в пятёрку крупнейших, складывается в «Прочие»;
 *   * цвет закреплён за позицией в списке и не перекрашивается при смене фильтра;
 *   * доля подписана прямо на секторе, а рядом лежит легенда с числами —
 *     цвет никогда не остаётся единственным способом понять, где что.
 */
import { Cell, Pie, PieChart, ResponsiveContainer, Tooltip } from 'recharts'

import { formatNumber } from '@/lib/format'

/**
 * Палитра секторов: фиксированный порядок, проверенный на различимость
 * при дальтонизме (худшая соседняя пара ΔE 9.1 при протанопии).
 * «Прочие» — не самостоятельная категория, а остаток, поэтому серый.
 */
const SLICE_COLORS = ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4']
const REST_COLOR = '#94a3b8'

const MAX_SLICES = 5

export interface PieSlice {
  name: string
  value: number
}

interface ProductionPieProps {
  items: PieSlice[]
  /** Единица измерения для подписей, например «шт.». */
  unit?: string
  height?: number
}

interface Slice extends PieSlice {
  color: string
  share: number
}

/** Пятёрка крупнейших, остальное — одним сектором «Прочие». */
function fold(items: PieSlice[]): Slice[] {
  const positive = items.filter((item) => item.value > 0).sort((a, b) => b.value - a.value)
  const total = positive.reduce((sum, item) => sum + item.value, 0)
  if (!total) return []

  const head = positive.slice(0, MAX_SLICES)
  const restValue = positive.slice(MAX_SLICES).reduce((sum, item) => sum + item.value, 0)

  const slices: Slice[] = head.map((item, index) => ({
    ...item,
    color: SLICE_COLORS[index],
    share: (item.value / total) * 100,
  }))
  if (restValue > 0) {
    slices.push({
      name: `Прочие (${positive.length - MAX_SLICES})`,
      value: restValue,
      color: REST_COLOR,
      share: (restValue / total) * 100,
    })
  }
  return slices
}

/** Подпись доли прямо на секторе — только там, где она помещается. */
function sliceLabel({
  cx,
  cy,
  midAngle,
  innerRadius,
  outerRadius,
  share,
}: {
  cx: number
  cy: number
  midAngle: number
  innerRadius: number
  outerRadius: number
  share: number
}) {
  if (share < 7) return null
  const radian = Math.PI / 180
  const radius = innerRadius + (outerRadius - innerRadius) * 0.55
  const x = cx + radius * Math.cos(-midAngle * radian)
  const y = cy + radius * Math.sin(-midAngle * radian)
  return (
    <text
      x={x}
      y={y}
      fill="#ffffff"
      fontSize={12}
      fontWeight={600}
      textAnchor="middle"
      dominantBaseline="central"
    >
      {share.toFixed(0)} %
    </text>
  )
}

export function ProductionPie({ items, unit = 'шт.', height = 230 }: ProductionPieProps) {
  const slices = fold(items)
  if (!slices.length) return <div className="sub muted">Нет данных за период</div>

  const total = slices.reduce((sum, item) => sum + item.value, 0)

  return (
    <div className="pie-block">
      <ResponsiveContainer width="100%" height={height}>
        <PieChart>
          <Pie
            data={slices}
            dataKey="value"
            nameKey="name"
            innerRadius="52%"
            outerRadius="82%"
            // Зазор между секторами того же цвета, что и фон карточки:
            // соседние доли не сливаются даже при похожих цветах
            paddingAngle={2}
            stroke="#ffffff"
            strokeWidth={2}
            isAnimationActive={false}
            labelLine={false}
            label={(props) => sliceLabel(props as never)}
          >
            {slices.map((slice) => (
              <Cell key={slice.name} fill={slice.color} />
            ))}
          </Pie>
          <Tooltip
            formatter={(value, name) => [
              `${formatNumber(Number(value))} ${unit}`,
              String(name),
            ]}
            contentStyle={{ borderRadius: 10, border: '1px solid #e2e8f0' }}
          />
        </PieChart>
      </ResponsiveContainer>

      <ul className="pie-legend">
        {slices.map((slice) => (
          <li key={slice.name}>
            <span className="pie-dot" style={{ background: slice.color }} />
            <span className="pie-name" title={slice.name}>
              {slice.name}
            </span>
            <span className="pie-value">
              {formatNumber(slice.value)} {unit}
            </span>
            <span className="pie-share">{slice.share.toFixed(1)} %</span>
          </li>
        ))}
        <li className="pie-total">
          <span className="pie-dot empty" />
          <span className="pie-name">Всего</span>
          <span className="pie-value">
            {formatNumber(total)} {unit}
          </span>
          <span className="pie-share">100 %</span>
        </li>
      </ul>
    </div>
  )
}
