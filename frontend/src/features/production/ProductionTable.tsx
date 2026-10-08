/**
 * Таблица плана и факта с правкой прямо в строке.
 *
 * Нажатие «править» превращает ячейки строки в поля ввода: план, факт, возврат,
 * станок и примечание можно поправить, не открывая отдельное окно. Остальные строки
 * при этом остаются как есть, поэтому видно, что меняешь и на фоне чего.
 *
 * Пустое поле означает «значения нет» и сохраняется именно так: «плана не было»
 * и «план равен нулю» — разные вещи, и процент выполнения ведёт себя по-разному.
 */
import { useState } from 'react'

import { productionApi } from '@/api'
import { Bar } from '@/components/ui'
import { formatDate, formatNumber } from '@/lib/format'
import type { MachineBinding, ProductionRow } from '@/types/api'

interface ProductionTableProps {
  rows: ProductionRow[]
  machines: MachineBinding[]
  canEdit: boolean
  onChanged: () => void
  onError: (message: string) => void
}

interface Draft {
  plan: string
  fact: string
  returned: string
  machineKey: string
  note: string
}

function draftFrom(row: ProductionRow): Draft {
  return {
    plan: row.plan_qty == null ? '' : String(row.plan_qty),
    fact: row.fact_qty == null ? '' : String(row.fact_qty),
    returned: row.return_qty == null ? '' : String(row.return_qty),
    machineKey: row.machine_key ?? '',
    note: row.note ?? '',
  }
}

/** Пустая строка — это «нет значения», а не ноль. */
function number(value: string): number | null {
  return value.trim() === '' ? null : Number(value)
}

function percentText(value: number | null): string {
  return value === null ? '—' : `${value.toFixed(1)} %`
}

export function ProductionTable({
  rows,
  machines,
  canEdit,
  onChanged,
  onError,
}: ProductionTableProps) {
  const [editingId, setEditingId] = useState<number | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [saving, setSaving] = useState(false)

  function startEdit(row: ProductionRow): void {
    setEditingId(row.id)
    setDraft(draftFrom(row))
  }

  function cancel(): void {
    setEditingId(null)
    setDraft(null)
  }

  async function save(row: ProductionRow): Promise<void> {
    if (!draft) return
    setSaving(true)
    try {
      await productionApi.updateRow(row.id, {
        plan_qty: number(draft.plan),
        fact_qty: number(draft.fact),
        return_qty: number(draft.returned),
        machine_key: draft.machineKey || null,
        note: draft.note || null,
      })
      cancel()
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  async function remove(row: ProductionRow): Promise<void> {
    if (!window.confirm(`Удалить строку «${row.product}»?`)) return
    try {
      await productionApi.removeRow(row.id)
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    }
  }

  const machineTitle = (key: string | null): string => {
    if (!key) return '—'
    return machines.find((machine) => machine.machine_key === key)?.title ?? key
  }

  const totals = rows.reduce(
    (acc, row) => ({
      plan: acc.plan + (row.plan_qty || 0),
      fact: acc.fact + (row.fact_qty || 0),
      returned: acc.returned + (row.return_qty || 0),
    }),
    { plan: 0, fact: 0, returned: 0 },
  )
  const totalPercent = totals.plan ? (totals.fact / totals.plan) * 100 : null

  return (
    <div className="table-wrap">
      <table className="table production-table">
        <thead>
          <tr>
            <th>Изделие</th>
            <th>Цех</th>
            <th>Станок</th>
            <th>Период</th>
            <th className="num">План</th>
            <th className="num">Факт</th>
            <th className="num">Выполнение</th>
            <th className="bar-col">Доля</th>
            <th className="num">Возврат</th>
            <th>Примечание</th>
            <th>Источник</th>
            {canEdit && <th />}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const editing = editingId === row.id && draft !== null
            return (
              <tr key={row.id} className={editing ? 'editing' : ''}>
                <td className="ellipsis" title={row.product}>
                  {row.product}
                  {row.body_type && <div className="sub">{row.body_type}</div>}
                </td>
                <td className="muted ellipsis" title={row.workshop ?? ''}>
                  {row.workshop || '—'}
                </td>

                <td className="muted">
                  {editing ? (
                    <select
                      value={draft.machineKey}
                      onChange={(event) =>
                        setDraft({ ...draft, machineKey: event.target.value })
                      }
                    >
                      <option value="">— без станка —</option>
                      {machines.map((machine) => (
                        <option key={machine.machine_key} value={machine.machine_key}>
                          {machine.title}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <span className="ellipsis">{machineTitle(row.machine_key)}</span>
                  )}
                </td>

                {/* Период в две строки: так колонка вдвое уже и строка таблицы
                    целиком помещается на экран вместе с кнопками правки */}
                <td className="muted nowrap">
                  {formatDate(row.period_from)}
                  <div className="sub">{formatDate(row.period_to)}</div>
                </td>

                <td className="num">
                  {editing ? (
                    <input
                      type="number"
                      min="0"
                      step="any"
                      className="cell-input"
                      value={draft.plan}
                      onChange={(event) => setDraft({ ...draft, plan: event.target.value })}
                    />
                  ) : row.plan_qty === null ? (
                    '—'
                  ) : (
                    formatNumber(row.plan_qty)
                  )}
                </td>

                <td className="num">
                  {editing ? (
                    <input
                      type="number"
                      min="0"
                      step="any"
                      className="cell-input"
                      value={draft.fact}
                      onChange={(event) => setDraft({ ...draft, fact: event.target.value })}
                    />
                  ) : row.fact_qty === null ? (
                    '—'
                  ) : (
                    formatNumber(row.fact_qty)
                  )}
                </td>

                <td className="num">{percentText(row.percent)}</td>
                <td className="bar-col">
                  <Bar
                    value={row.percent ?? 0}
                    color={
                      row.percent === null
                        ? 'var(--muted)'
                        : row.percent >= 100
                          ? 'var(--good)'
                          : 'var(--warn)'
                    }
                  />
                </td>

                <td className="num muted">
                  {editing ? (
                    <input
                      type="number"
                      min="0"
                      step="any"
                      className="cell-input"
                      value={draft.returned}
                      onChange={(event) => setDraft({ ...draft, returned: event.target.value })}
                    />
                  ) : row.return_qty ? (
                    formatNumber(row.return_qty)
                  ) : (
                    '—'
                  )}
                </td>

                <td className="muted ellipsis" title={row.note ?? ''}>
                  {editing ? (
                    <input
                      className="cell-input wide"
                      value={draft.note}
                      onChange={(event) => setDraft({ ...draft, note: event.target.value })}
                    />
                  ) : (
                    row.note || '—'
                  )}
                </td>

                <td>
                  <span
                    className={`status status-${row.source === 'manual' ? 'ok' : 'pending'}`}
                  >
                    {row.source === 'manual' ? 'вручную' : 'рапорт'}
                  </span>
                </td>

                {canEdit && (
                  <td className="row-actions">
                    {editing ? (
                      <>
                        <button
                          type="button"
                          className="btn link"
                          disabled={saving}
                          onClick={() => void save(row)}
                        >
                          {saving ? '…' : 'сохранить'}
                        </button>
                        <button type="button" className="btn link" onClick={cancel}>
                          отмена
                        </button>
                      </>
                    ) : (
                      <>
                        <button
                          type="button"
                          className="btn link"
                          onClick={() => startEdit(row)}
                        >
                          править
                        </button>
                        <button
                          type="button"
                          className="btn link danger"
                          onClick={() => void remove(row)}
                        >
                          удалить
                        </button>
                      </>
                    )}
                  </td>
                )}
              </tr>
            )
          })}
        </tbody>
        <tfoot>
          <tr>
            <td colSpan={4}>Итого строк: {formatNumber(rows.length)}</td>
            <td className="num">{formatNumber(totals.plan)}</td>
            <td className="num">{formatNumber(totals.fact)}</td>
            <td className="num">{percentText(totalPercent)}</td>
            <td />
            <td className="num">{formatNumber(totals.returned)}</td>
            <td colSpan={canEdit ? 3 : 2} />
          </tr>
        </tfoot>
      </table>
    </div>
  )
}
