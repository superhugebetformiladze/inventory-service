/** Надпись «Загрузка…» на время ожидания ответа сервера. */
import type { ReactNode } from 'react'

export function Loader({ children = 'Загрузка…' }: { children?: ReactNode }) {
  return <div className="loader">{children}</div>
}
