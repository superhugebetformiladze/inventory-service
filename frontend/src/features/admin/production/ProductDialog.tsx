/**
 * Окно создания и правки изделия.
 *
 * Название, тип корпуса и цех вместе служат ключом: по ним повторная загрузка
 * рапорта находит уже заведённое изделие. Поэтому, меняя их, стоит понимать, что
 * следующий рапорт с прежними названиями заведёт изделие заново.
 */
import { useState } from 'react'

import { productionApi } from '@/api'
import type { Product } from '@/types/api'

interface ProductDialogProps {
  /** Изделие для правки или null, если создаём новое. */
  product: Product | null
  onCancel: () => void
  onSaved: () => void
  onError: (message: string) => void
}

export function ProductDialog({ product, onCancel, onSaved, onError }: ProductDialogProps) {
  const [name, setName] = useState(product?.name ?? '')
  const [bodyType, setBodyType] = useState(product?.body_type ?? '')
  const [workshop, setWorkshop] = useState(product?.workshop ?? '')
  const [code, setCode] = useState(product?.code ?? '')
  const [unit, setUnit] = useState(product?.unit ?? 'шт.')
  const [notes, setNotes] = useState(product?.notes ?? '')
  const [saving, setSaving] = useState(false)

  async function submit(event: React.FormEvent): Promise<void> {
    event.preventDefault()
    setSaving(true)
    const payload = {
      name,
      body_type: bodyType || null,
      workshop: workshop || null,
      code: code || null,
      unit: unit || 'шт.',
      notes: notes || null,
    }
    try {
      if (product) await productionApi.updateProduct(product.id, payload)
      else await productionApi.createProduct(payload)
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
        <h2>{product ? `Изделие: ${product.name}` : 'Новое изделие'}</h2>

        <div className="modal-grid">
          <label className="field wide">
            <span>Название</span>
            <input value={name} required onChange={(event) => setName(event.target.value)} />
          </label>
          <label className="field">
            <span>Тип корпуса</span>
            <input value={bodyType} onChange={(event) => setBodyType(event.target.value)} />
          </label>
          <label className="field">
            <span>Цех</span>
            <input value={workshop} onChange={(event) => setWorkshop(event.target.value)} />
          </label>
          <label className="field">
            <span>Код</span>
            <input value={code} onChange={(event) => setCode(event.target.value)} />
          </label>
          <label className="field">
            <span>Единица измерения</span>
            <input value={unit} onChange={(event) => setUnit(event.target.value)} />
          </label>
          <label className="field wide">
            <span>Примечание</span>
            <textarea rows={2} value={notes} onChange={(event) => setNotes(event.target.value)} />
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
