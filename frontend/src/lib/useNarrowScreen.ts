/** Определяет узкий экран (телефон), чтобы графики рисовались компактнее. */
import { useEffect, useState } from 'react'

/** Ширина экрана — от неё зависят подписи осей и высота графиков. */
export function useNarrowScreen(breakpoint = 760): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof window !== 'undefined' && window.innerWidth < breakpoint,
  )

  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth < breakpoint)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [breakpoint])

  return narrow
}
