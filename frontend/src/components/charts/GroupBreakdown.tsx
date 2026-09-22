/**
 * Структура времени по группам состояний.
 *
 * Одна полоса на 100 % и расшифровка списком. Раньше здесь была круговая диаграмма,
 * но названия групп длинные — легенда наезжала на сам график, читать было неудобно.
 */
import { formatHours, formatPercent } from '@/lib/format'
import { groupColor, groupShort, sortGroups } from '@/lib/groups'
import type { GroupShare } from '@/types/api'

interface GroupBreakdownProps {
  items: GroupShare[]
  totalSeconds: number
}

export function GroupBreakdown({ items, totalSeconds }: GroupBreakdownProps) {
  const ordered = sortGroups(items)

  return (
    <div className="breakdown">
      <div className="breakdown-bar">
        {ordered.map((item) => (
          <div
            key={item.group}
            className="breakdown-seg"
            style={{ width: `${item.share}%`, background: groupColor(item.group, item.ext_id) }}
            title={`${item.group}\n${formatHours(item.seconds)} · ${formatPercent(item.share)}`}
          >
            {item.share >= 7 && <span>{formatPercent(item.share, 0)}</span>}
          </div>
        ))}
      </div>

      <div className="breakdown-scale">
        <span>0 %</span>
        <span>50 %</span>
        <span>100 %</span>
      </div>

      <ul className="breakdown-list">
        {ordered.map((item) => (
          <li key={item.group}>
            <span className="dot" style={{ background: groupColor(item.group, item.ext_id) }} />
            <span className="breakdown-name">
              <strong>{groupShort(item.group, item.ext_id)}</strong>
              <em>{item.group}</em>
            </span>
            <span className="breakdown-hours">{formatHours(item.seconds, 0)}</span>
            <span className="breakdown-share">{formatPercent(item.share)}</span>
          </li>
        ))}
      </ul>

      {totalSeconds > 0 && (
        <p className="hint">
          Всего учтено {formatHours(totalSeconds, 0)} — он же знаменатель всех долей
        </p>
      )}
    </div>
  )
}
