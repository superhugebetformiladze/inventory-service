/**
 * Ручной ввод строки плана и факта.
 *
 * Нужен, когда рапорта нет или когда выпуск надо записать на конкретный станок —
 * в рапорте станков нет, он составляется по цехам. Изделие берётся из справочника
 * либо заводится прямо здесь.
 */
import { useEffect, useState } from 'react'

import { equipmentApi, productionApi } from '@/api'
import type { MachineBinding, Product, ProductionRow } from '@/types/api'

interface RowDialogProps {
  /** Строка для правки или null, если создаём новую. */
  row: ProductionRow | null
  defaultFrom: string
  defaultTo: string
  onCancel: () => void
  onSaved: () => void
  onError: (message: string) => void
}

const NEW_PRODUCT = 'new'

export function RowDialog({
  row,
  defaultFrom,
  defaultTo,
  onCancel,
  onSaved,
  onError,
}: RowDialogProps) {
  const [products, setProducts] = useState<Product[]>([])
  const [machines, setMachines] = useState<MachineBinding[]>([])
  const [saving, setSaving] = useState(false)

  const [productId, setProductId] = useState<string>(row ? String(row.product_id) : '')
  const [productName, setProductName] = useState('')
  const [bodyType, setBodyType] = useState('')
  const [workshop, setWorkshop] = useState(row?.workshop ?? '')
  const [machineKey, setMachineKey] = useState(row?.machine_key ?? '')
  const [periodFrom, setPeriodFrom] = useState(row?.period_from ?? defaultFrom)
  const [periodTo, setPeriodTo] = useState(row?.period_to ?? defaultTo)
  const [plan, setPlan] = useState(row?.plan_qty == null ? '' : String(row.plan_qty))
  const [fact, setFact] = useState(row?.fact_qty == null ? '' : String(row.fact_qty))
  const [returned, setReturned] = useState(row?.return_qty == null ? '' : String(row.return_qty))
  const [note, setNote] = useState(row?.note ?? '')

  useEffect(() => {
    Promise.all([productionApi.products(), equipmentApi.machines()])
      .then(([items, bindings]) => {
        setProducts(items)
        setMachines(bindings)
      })
      .catch((err) => onError(err instanceof Error ? err.message : String(err)))
    // справочники нужны один раз при открытии окна
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  /** Пустое поле означает «значения нет», а не ноль — так и отправляем. */
  const number = (value: string): number | null => (value.trim() === '' ? null : Number(value))

  async function submit(event: React.FormEvent): Promise<void> {
    event.preventDefault()
    if (periodTo < periodFrom) {
      onError('Конец периода раньше его начала')
      return
    }
    setSaving(true)
    try {
      if (row) {
        await productionApi.updateRow(row.id, {
          machine_key: machineKey || null,
          period_from: periodFrom,
          period_to: periodTo,
          plan_qty: number(plan),
          fact_qty: number(fact),
          return_qty: number(returned),
          note: note || null,
        })
      } else {
        await productionApi.createRow({
          product_id: productId && productId !== NEW_PRODUCT ? Number(productId) : undefined,
          product_name: productId === NEW_PRODUCT ? productName : undefined,
          body_type: productId === NEW_PRODUCT ? bodyType || null : undefined,
          workshop: workshop || null,
          machine_key: machineKey || null,
          period_from: periodFrom,
          period_to: periodTo,
          plan_qty: number(plan),
          fact_qty: number(fact),
          return_qty: number(returned),
          note: note || null,
        })
      }
      onSaved()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="modal-backdrop" onClick={onCancel}>
      <form className="modal" onClick={(event) => event.stopPropagation()} onSubmit={submit}>
        <h2>{row ? `Строка: ${row.product}` : 'Новая строка плана и факта'}</h2>

        <div className="modal-grid">
          {!row && (
            <>
              <label className="field wide">
                <span>Изделие</span>
                <select
                  value={productId}
                  required
                  onChange={(event) => setProductId(event.target.value)}
                >
                  <option value="">— выберите изделие —</option>
                  <option value={NEW_PRODUCT}>+ новое изделие</option>
                  {products.map((product) => (
                    <option key={product.id} value={product.id}>
                      {product.title}
                      {product.workshop ? ` · ${product.workshop}` : ''}
                    </option>
                  ))}
                </select>
              </label>

              {productId === NEW_PRODUCT && (
                <>
                  <label className="field">
                    <span>Название изделия</span>
                    <input
                      value={productName}
                      required
                      onChange={(event) => setProductName(event.target.value)}
                    />
                  </label>
                  <label className="field">
                    <span>Тип корпуса</span>
                    <input
                      value={bodyType}
                      onChange={(event) => setBodyType(event.target.value)}
                    />
                  </label>
                </>
              )}

              <label className="field">
                <span>Цех</span>
                <input value={workshop} onChange={(event) => setWorkshop(event.target.value)} />
              </label>
            </>
          )}

          <label className="field">
            <span>Станок</span>
            <select value={machineKey} onChange={(event) => setMachineKey(event.target.value)}>
              <option value="">— без привязки к станку —</option>
              {machines.map((machine) => (
                <option key={machine.machine_key} value={machine.machine_key}>
                  {machine.title}
                </option>
              ))}
            </select>
          </label>

          <label className="field">
            <span>Период с</span>
            <input
              type="date"
              value={periodFrom}
              required
              onChange={(event) => setPeriodFrom(event.target.value)}
            />
          </label>
          <label className="field">
            <span>Период по</span>
            <input
              type="date"
              value={periodTo}
              required
              onChange={(event) => setPeriodTo(event.target.value)}
            />
          </label>

          <label className="field">
            <span>План, шт.</span>
            <input
              type="number"
              min="0"
              step="any"
              value={plan}
              onChange={(event) => setPlan(event.target.value)}
            />
          </label>
          <label className="field">
            <span>Факт, шт.</span>
            <input
              type="number"
              min="0"
              step="any"
              value={fact}
              onChange={(event) => setFact(event.target.value)}
            />
          </label>
          <label className="field">
            <span>Возврат, шт.</span>
            <input
              type="number"
              min="0"
              step="any"
              value={returned}
              onChange={(event) => setReturned(event.target.value)}
            />
          </label>

          <label className="field wide">
            <span>Примечание</span>
            <textarea rows={2} value={note} onChange={(event) => setNote(event.target.value)} />
          </label>
        </div>

        <div className="modal-actions">
          <button type="button" className="btn ghost" onClick={onCancel}>
            Отмена
          </button>
          <button type="submit" className="btn primary" disabled={saving}>
            {saving ? 'Сохраняем…' : 'Сохранить'}
          </button>
        </div>
      </form>
    </div>
  )
}
