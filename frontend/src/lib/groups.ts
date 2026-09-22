/**
 * Цвет и порядок групп состояний закреплены за смыслом, а не за величиной доли:
 * «машинное время» всегда зелёное, «выключено» — серое, простои — тёплые цвета.
 * Сопоставление идёт по ключевому слову в названии группы, с запасным вариантом
 * по GroupParam.ID из XML, поэтому переименования в системе мониторинга не ломают легенду.
 */

interface Category {
  key: string
  match: RegExp
  extId: number
  color: string
  short: string
  rank: number
}

const CATEGORIES: Category[] = [
  { key: 'work', match: /машинн/i, extId: 2, color: '#16a34a', short: 'Тмаш', rank: 0 },
  { key: 'aux', match: /вспомогат/i, extId: 3, color: '#0ea5e9', short: 'Твсп+Тпз', rank: 1 },
  { key: 'technical', match: /техническ/i, extId: 5, color: '#dc2626', short: 'Тпрос. техн.', rank: 2 },
  { key: 'organizational', match: /организац/i, extId: 6, color: '#f97316', short: 'Тпрос. орг.', rank: 3 },
  { key: 'unplanned', match: /нерегламент/i, extId: 4, color: '#a855f7', short: 'Тпрос. н/р', rank: 4 },
  { key: 'maintenance', match: /обслуж/i, extId: 7, color: '#0d9488', short: 'Тобсл', rank: 5 },
  { key: 'off', match: /исходн|выкл/i, extId: 1, color: '#94a3b8', short: 'Твыкл', rank: 6 },
]

const FALLBACK_COLORS = ['#6366f1', '#eab308', '#ec4899', '#14b8a6', '#64748b']

function category(name: string | null | undefined, extId: number | null | undefined): Category | null {
  const byName = CATEGORIES.find((item) => item.match.test(name ?? ''))
  if (byName) return byName
  return CATEGORIES.find((item) => item.extId === extId) ?? null
}

export function groupColor(name: string | null | undefined, extId: number | null | undefined): string {
  const found = category(name, extId)
  if (found) return found.color
  const hash = [...(name ?? '')].reduce((acc, char) => acc + char.charCodeAt(0), 0)
  return FALLBACK_COLORS[hash % FALLBACK_COLORS.length]
}

export function groupShort(name: string | null | undefined, extId: number | null | undefined): string {
  const found = category(name, extId)
  if (found) return found.short
  if (!name) return '—'
  return name.length > 16 ? `${name.slice(0, 15)}…` : name
}

export function groupRank(name: string | null | undefined, extId: number | null | undefined): number {
  const found = category(name, extId)
  return found ? found.rank : 90
}

/** Группы в осмысленном порядке: работа -> вспомогательные -> простои -> выключено. */
export function sortGroups<T extends { group: string; ext_id: number | null }>(items: T[]): T[] {
  return [...items].sort((a, b) => {
    const rankDiff = groupRank(a.group, a.ext_id) - groupRank(b.group, b.ext_id)
    if (rankDiff !== 0) return rankDiff
    return a.group.localeCompare(b.group, 'ru')
  })
}
