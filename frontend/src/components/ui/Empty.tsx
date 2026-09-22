/** Заглушка на месте пустого блока: «данных нет». */
import type { ReactNode } from 'react'

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>
}
