/**
 * Окно редактирования карточки оборудования.
 *
 * Список полей задан один раз в FIELDS — из него строится и форма, и отправляемые
 * на сервер данные. Чтобы добавить новое поле в карточку, достаточно дописать строку.
 */
import { useState } from 'react'

import type { EquipmentPatch } from '@/api/equipment'
import type { EquipmentCard } from '@/types/api'

type FieldType = 'text' | 'number' | 'date' | 'textarea'

/** Поля карточки: ключ, подпись, тип. Порядок задаёт вид формы. */
export const FIELDS: [keyof EquipmentPatch, string, FieldType][] = [
  ['inv_number', 'Инвентарный номер', 'text'],
  ['display_name', 'Отображаемое название', 'text'],
  ['name', 'Наименование', 'text'],
  ['model', 'Модель', 'text'],
  ['cnc_system', 'Система ЧПУ', 'text'],
  ['mdm_code', 'Код MDM', 'text'],
  ['monitoring_id', 'ID в мониторинге', 'number'],
  ['ip_address', 'IP-адрес', 'text'],
  ['cnc_name', 'Имя УЧПУ', 'text'],
  ['cnc_type', 'Тип УЧПУ', 'text'],
  ['shop_number', 'Номер в цехе', 'text'],
  ['workshop', 'Цех', 'text'],
  ['location', 'Расположение', 'text'],
  ['serial_number', 'Заводской номер', 'text'],
  ['manufacturer', 'Производитель', 'text'],
  ['year_made', 'Год выпуска', 'number'],
  ['commissioned_on', 'Ввод в эксплуатацию', 'date'],
  ['responsible', 'Ответственный', 'text'],
  ['status', 'Состояние', 'text'],
  ['notes', 'Примечание', 'textarea'],
]

interface EquipmentDialogProps {
  card: Partial<EquipmentCard>
  isNew?: boolean
  onCancel: () => void
  onSave: (values: EquipmentPatch) => void
}

export function EquipmentDialog({ card, isNew = false, onCancel, onSave }: EquipmentDialogProps) {
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      FIELDS.map(([key]) => [key, card[key as keyof EquipmentCard] == null ? '' : String(card[key as keyof EquipmentCard])]),
    ),
  )

  function submit(event: React.FormEvent) {
    event.preventDefault()
    const payload: Record<string, unknown> = {}
    FIELDS.forEach(([key, , type]) => {
      const value = values[key as string]
      if (value === '' || value === undefined) {
        // У новой карточки пустые поля просто не отправляем, у существующей — очищаем.
        if (!isNew) payload[key as string] = null
        return
      }
      payload[key as string] = type === 'number' ? Number(value) : value
    })
    onSave(payload as EquipmentPatch)
  }

  return (
    <div className="modal-backdrop" onClick={onCancel}>
      <form className="modal" onClick={(event) => event.stopPropagation()} onSubmit={submit}>
        <h2>{isNew ? 'Новая карточка' : `Карточка ${card.inv_number}`}</h2>

        <div className="modal-grid">
          {FIELDS.map(([key, label, type]) => (
            <label key={key as string} className={`field ${type === 'textarea' ? 'wide' : ''}`}>
              <span>{label}</span>
              {type === 'textarea' ? (
                <textarea
                  rows={2}
                  value={values[key as string] ?? ''}
                  onChange={(event) =>
                    setValues({ ...values, [key as string]: event.target.value })
                  }
                />
              ) : (
                <input
                  type={type}
                  value={values[key as string] ?? ''}
                  required={key === 'inv_number'}
                  onChange={(event) =>
                    setValues({ ...values, [key as string]: event.target.value })
                  }
                />
              )}
            </label>
          ))}
        </div>

        <div className="modal-actions">
          <button type="button" className="btn ghost" onClick={onCancel}>
            Отмена
          </button>
          <button type="submit" className="btn primary">
            Сохранить
          </button>
        </div>
      </form>
    </div>
  )
}
