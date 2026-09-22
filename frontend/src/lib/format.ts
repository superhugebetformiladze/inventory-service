/** Форматирование чисел, времени и дат для интерфейса. */

export function hours(seconds: number | null | undefined): number {
  return (seconds || 0) / 3600
}

export function formatHours(seconds: number | null | undefined, digits = 1): string {
  return `${hours(seconds).toLocaleString('ru-RU', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })} ч`
}

export function formatDuration(seconds: number | null | undefined): string {
  const total = Math.round(seconds || 0)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  if (h === 0) return `${m} мин`
  return `${h} ч ${String(m).padStart(2, '0')} мин`
}

export function formatPercent(value: number | null | undefined, digits = 1): string {
  return `${(value || 0).toFixed(digits)} %`
}

export function formatDate(value: string | null | undefined): string {
  if (!value) return '—'
  return new Date(value).toLocaleDateString('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
  })
}

export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—'
  return new Date(value).toLocaleString('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function formatSize(bytes: number | null | undefined): string {
  if (!bytes) return '—'
  const mb = bytes / (1024 * 1024)
  if (mb >= 1) return `${mb.toFixed(1)} МБ`
  return `${(bytes / 1024).toFixed(0)} КБ`
}

export function formatNumber(value: number | null | undefined): string {
  return (value || 0).toLocaleString('ru-RU')
}

/** Дата в формате input[type=date]. */
export function toInputDate(value: string | null | undefined): string {
  if (!value) return ''
  return new Date(value).toISOString().slice(0, 10)
}

/** Длинные IntegrationID на узких экранах режем слева — значимая часть в хвосте. */
export function shortenLabel(value: string | number | null | undefined, max = 14): string {
  const text = String(value ?? '')
  return text.length > max ? `…${text.slice(-max)}` : text
}

/** Цвет ячейки по коэффициенту загрузки: от красного к зелёному. */
export function utilizationColor(value: number): string {
  if (value >= 60) return '#16a34a'
  if (value >= 40) return '#65a30d'
  if (value >= 20) return '#f59e0b'
  if (value > 0) return '#ea580c'
  return '#e2e8f0'
}
