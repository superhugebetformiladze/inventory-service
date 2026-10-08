/**
 * Справочник изделий и закрепление их за станками.
 *
 * Номенклатура пополняется сама при загрузке рапорта, здесь её можно поправить
 * и, главное, указать, на каких станках изделие делают: в рапорте станков нет,
 * он составляется по цехам. Одно изделие может выпускаться на нескольких станках,
 * и на одном станке может делаться несколько изделий.
 */
import { useCallback, useEffect, useMemo, useState } from 'react'

import { equipmentApi, productionApi } from '@/api'
import { Alert, Card, Empty, Loader } from '@/components/ui'
import type { MachineBinding, MachineLink, Product } from '@/types/api'

import { ProductDialog } from './ProductDialog'

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function ProductsSection() {
  const [products, setProducts] = useState<Product[]>([])
  const [links, setLinks] = useState<MachineLink[]>([])
  const [machines, setMachines] = useState<MachineBinding[]>([])

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [search, setSearch] = useState('')
  const [workshop, setWorkshop] = useState('')
  const [editing, setEditing] = useState<Product | 'new' | null>(null)
  /** Для какого изделия сейчас открыт выбор станка. */
  const [linking, setLinking] = useState<number | null>(null)

  const load = useCallback(async () => {
    const [items, bound, bindings] = await Promise.all([
      productionApi.products(),
      productionApi.links(),
      equipmentApi.machines(),
    ])
    setProducts(items)
    setLinks(bound)
    setMachines(bindings)
  }, [])

  useEffect(() => {
    setLoading(true)
    load()
      .catch((err) => setError(message(err)))
      .finally(() => setLoading(false))
  }, [load])

  /** machine_key -> человеческое название станка. */
  const titles = useMemo(
    () => new Map(machines.map((machine) => [machine.machine_key, machine.title])),
    [machines],
  )

  /** product_id -> его привязки к станкам. */
  const byProduct = useMemo(() => {
    const map = new Map<number, MachineLink[]>()
    links.forEach((item) => {
      const list = map.get(item.product_id) ?? []
      list.push(item)
      map.set(item.product_id, list)
    })
    return map
  }, [links])

  const workshops = useMemo(
    () => Array.from(new Set(products.map((item) => item.workshop).filter(Boolean))) as string[],
    [products],
  )

  const visible = products.filter((product) => {
    const text = `${product.name} ${product.body_type ?? ''} ${product.code ?? ''}`.toLowerCase()
    const matchesSearch = !search || text.includes(search.toLowerCase())
    const matchesShop = !workshop || product.workshop === workshop
    return matchesSearch && matchesShop
  })

  async function addLink(productId: number, machineKey: string): Promise<void> {
    if (!machineKey) return
    try {
      await productionApi.link(machineKey, productId)
      setLinking(null)
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  async function dropLink(link: MachineLink): Promise<void> {
    try {
      await productionApi.unlink(link.id)
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  async function removeProduct(product: Product): Promise<void> {
    if (
      !window.confirm(
        `Удалить изделие «${product.title}»? Вместе с ним пропадут его строки плана и факта.`,
      )
    )
      return
    try {
      await productionApi.removeProduct(product.id)
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  return (
    <>
      <Alert onClose={() => setError('')}>{error}</Alert>

      <Card
        title="Номенклатура"
        subtitle="изделия из рапортов и закрепление их за станками"
        className="wide"
        actions={
          <button type="button" className="btn primary" onClick={() => setEditing('new')}>
            Добавить изделие
          </button>
        }
      >
        <div className="period-row">
          <label className="field inline">
            <span>Поиск</span>
            <input
              value={search}
              placeholder="название или тип корпуса"
              onChange={(event) => setSearch(event.target.value)}
            />
          </label>
          <label className="field inline">
            <span>Цех</span>
            <select value={workshop} onChange={(event) => setWorkshop(event.target.value)}>
              <option value="">Все</option>
              {workshops.map((item) => (
                <option key={item} value={item}>
                  {item}
                </option>
              ))}
            </select>
          </label>
          <span className="sub muted">
            изделий: {products.length}, закреплено за станками: {byProduct.size}
          </span>
        </div>

        {loading ? (
          <Loader />
        ) : visible.length === 0 ? (
          <Empty>
            {products.length === 0
              ? 'Номенклатуры пока нет — загрузите рапорт или добавьте изделие вручную'
              : 'Ничего не найдено'}
          </Empty>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Изделие</th>
                  <th>Тип корпуса</th>
                  <th>Цех</th>
                  <th>Ед.</th>
                  <th>Станки</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {visible.map((product) => {
                  const bound = byProduct.get(product.id) ?? []
                  return (
                    <tr key={product.id}>
                      <td className="ellipsis" title={product.name}>
                        {product.name}
                      </td>
                      <td className="muted ellipsis">{product.body_type || '—'}</td>
                      <td className="muted ellipsis">{product.workshop || '—'}</td>
                      <td className="muted">{product.unit}</td>
                      <td>
                        <div className="chip-row">
                          {bound.map((item) => (
                            <span key={item.id} className="chip">
                              {titles.get(item.machine_key) || item.machine_key}
                              <button
                                type="button"
                                className="chip-x"
                                title="Открепить от станка"
                                onClick={() => void dropLink(item)}
                              >
                                ×
                              </button>
                            </span>
                          ))}

                          {linking === product.id ? (
                            <select
                              autoFocus
                              defaultValue=""
                              onBlur={() => setLinking(null)}
                              onChange={(event) =>
                                void addLink(product.id, event.target.value)
                              }
                            >
                              <option value="">— выберите станок —</option>
                              {machines
                                .filter(
                                  (machine) =>
                                    !bound.some(
                                      (item) => item.machine_key === machine.machine_key,
                                    ),
                                )
                                .map((machine) => (
                                  <option key={machine.machine_key} value={machine.machine_key}>
                                    {machine.title}
                                  </option>
                                ))}
                            </select>
                          ) : (
                            <button
                              type="button"
                              className="btn link"
                              onClick={() => setLinking(product.id)}
                            >
                              + станок
                            </button>
                          )}
                        </div>
                      </td>
                      <td className="row-actions">
                        <button
                          type="button"
                          className="btn link"
                          onClick={() => setEditing(product)}
                        >
                          править
                        </button>
                        <button
                          type="button"
                          className="btn link danger"
                          onClick={() => void removeProduct(product)}
                        >
                          удалить
                        </button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {editing && (
        <ProductDialog
          product={editing === 'new' ? null : editing}
          onCancel={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void load()
          }}
          onError={setError}
        />
      )}
    </>
  )
}
