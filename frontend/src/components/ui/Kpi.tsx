/** Плитка с крупным показателем: подпись, значение и пояснение, как оно считается. */
import type { ReactNode } from 'react'

type Tone = 'default' | 'accent' | 'good' | 'warn'

interface KpiProps {
  label: ReactNode
  value: ReactNode
  /** Подпись под значением: здесь пишем, что на что делится. */
  hint?: ReactNode
  tone?: Tone
}

export function Kpi({ label, value, hint, tone = 'default' }: KpiProps) {
  return (
    <div className={`kpi kpi-${tone}`}>
      <div className="kpi-label">{label}</div>
      <div className="kpi-value">{value}</div>
      {hint && <div className="kpi-hint">{hint}</div>}
    </div>
  )
}
