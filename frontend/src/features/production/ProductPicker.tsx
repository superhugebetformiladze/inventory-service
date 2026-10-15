/**
 * Выбор изделий для фильтра: кнопка со списком-раскладушкой и галочками.
 *
 * Номенклатуры бывают сотни, поэтому изделия сгруппированы по цехам, внутри списка
 * есть своя строка поиска, а у каждого цеха — ссылка «выбрать весь цех». Пока ничего
 * не выбрано, показываются все изделия: пустой выбор означает «без ограничения»,
 * а не «ничего».
 */
import { useMemo, useState } from 'react'

import type { Product } from '@/types/api'

interface ProductPickerProps {
  products: Product[]
  /** Идентификаторы выбранных изделий; пустой список — показываем все. */
  selected: number[]
  onChange: (value: number[]) => void
}

export function ProductPicker({ products, selected, onChange }: ProductPickerProps) {
  const [open, setOpen] = useState(false)
  const [needle, setNeedle] = useState('')

  const visible = useMemo(() => {
    const text = needle.trim().toLowerCase()
    if (!text) return products
    return products.filter((product) =>
      `${product.name} ${product.body_type ?? ''} ${product.code ?? ''}`
        .toLowerCase()
        .includes(text),
    )
  }, [products, needle])

  /** Изделия, разложенные по цехам: цеха по алфавиту, «Без цеха» в конце. */
  const groups = useMemo(() => {
    const acc = new Map<string, Product[]>()
    visible.forEach((product) => {
      const shop = product.workshop || 'Без цеха'
      const list = acc.get(shop) ?? []
      list.push(product)
      acc.set(shop, list)
    })
    return [...acc].sort(([a], [b]) => {
      if (a === 'Без цеха') return 1
      if (b === 'Без цеха') return -1
      return a.localeCompare(b, 'ru')
    })
  }, [visible])

  function toggle(id: number): void {
    onChange(selected.includes(id) ? selected.filter((item) => item !== id) : [...selected, id])
  }

  const label = selected.length ? `выбрано ${selected.length}` : 'все изделия'

  return (
    <div className="machine-picker product-picker">
      <button type="button" className="btn ghost" onClick={() => setOpen((value) => !value)}>
        Изделия: {label} {open ? '▲' : '▼'}
      </button>

      {selected.length > 0 && (
        <button type="button" className="btn link" onClick={() => onChange([])}>
          сбросить
        </button>
      )}

      {open && (
        <div className="picker-panel">
          <div className="picker-head">
            <input
              className="picker-search"
              value={needle}
              placeholder="поиск по названию или типу корпуса"
              onChange={(event) => setNeedle(event.target.value)}
            />
            <button
              type="button"
              className="btn link"
              disabled={!visible.length}
              onClick={() => onChange([...new Set([...selected, ...visible.map((p) => p.id)])])}
            >
              выбрать всё найденное ({visible.length})
            </button>
          </div>

          {visible.length === 0 ? (
            <div className="sub muted">Ничего не найдено</div>
          ) : (
            <div className="picker-groups">
              {groups.map(([shop, items]) => {
                const ids = items.map((item) => item.id)
                const allPicked = ids.every((id) => selected.includes(id))
                return (
                  <section key={shop} className="picker-group">
                    <header className="picker-group-head">
                      <span className="picker-group-name" title={shop}>
                        {shop}
                      </span>
                      <span className="picker-group-count">{items.length}</span>
                      <button
                        type="button"
                        className="btn link"
                        onClick={() =>
                          onChange(
                            allPicked
                              ? selected.filter((id) => !ids.includes(id))
                              : [...new Set([...selected, ...ids])],
                          )
                        }
                      >
                        {allPicked ? 'снять цех' : 'выбрать цех'}
                      </button>
                    </header>

                    <div className="machine-list">
                      {items.map((product) => (
                        <label key={product.id} className="check">
                          <input
                            type="checkbox"
                            checked={selected.includes(product.id)}
                            onChange={() => toggle(product.id)}
                          />
                          <span>
                            {product.name}
                            {product.body_type && (
                              <span className="muted"> · {product.body_type}</span>
                            )}
                          </span>
                        </label>
                      ))}
                    </div>
                  </section>
                )
              })}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
