/** Тонкая полоска-индикатор внутри таблиц: наглядно показывает долю в процентах. */
interface BarProps {
  /** Значение в процентах, 0..100. */
  value: number
  color?: string | null
}

export function Bar({ value, color }: BarProps) {
  return (
    <div className="mini-bar">
      <span style={{ width: `${Math.min(value, 100)}%`, background: color || 'var(--accent)' }} />
    </div>
  )
}
