/**
 * Раздел админки «Оборудование»: карточки станков.
 *
 * Карточка — это паспорт станка: инвентарный номер, модель, система ЧПУ, IP и прочее.
 * Их загружают таблицей Excel, а потом при необходимости правят руками.
 *
 * Отдельная колонка — привязка карточки к станку из мониторинга. Если в таблице была
 * колонка ID, привязка точная. Если нет — система раскладывает карточки по порядку
 * и помечает такие привязки жёлтым: их нужно проверить глазами.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'

import { equipmentApi } from '@/api'
import type { EquipmentPatch } from '@/api/equipment'
import { Alert, Card, Empty, Loader } from '@/components/ui'
import type { BindingKind, EquipmentCard, ImportReport, MachineBinding } from '@/types/api'

import { EquipmentDialog } from './EquipmentDialog'
import { ImportSummary } from './ImportSummary'

const BINDING_LABEL: Record<BindingKind, string> = {
  auto: 'точная',
  guess: 'по порядку',
  manual: 'вручную',
  none: 'нет',
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function EquipmentPanel() {
  const [cards, setCards] = useState<EquipmentCard[]>([])
  const [machines, setMachines] = useState<MachineBinding[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [info, setInfo] = useState('')
  const [report, setReport] = useState<ImportReport | null>(null)
  const [editing, setEditing] = useState<EquipmentCard | null>(null)
  const [creating, setCreating] = useState(false)
  const [search, setSearch] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [cardRows, machineRows] = await Promise.all([
        equipmentApi.list(),
        equipmentApi.machines(),
      ])
      setCards(cardRows)
      setMachines(machineRows)
    } catch (err) {
      setError(message(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function upload(file: File | undefined): Promise<void> {
    if (!file) return
    setError('')
    setInfo('')
    setReport(null)
    try {
      const result = await equipmentApi.importExcel(file)
      setReport(result)
      setInfo(`Импорт выполнен: создано ${result.created}, обновлено ${result.updated}.`)
      await load()
    } catch (err) {
      setError(message(err))
    } finally {
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  async function save(card: EquipmentCard, values: EquipmentPatch): Promise<void> {
    try {
      await equipmentApi.update(card.id, values)
      setEditing(null)
      setInfo('Карточка сохранена')
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  async function create(values: EquipmentPatch): Promise<void> {
    try {
      await equipmentApi.create(values)
      setCreating(false)
      setInfo('Карточка создана')
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  async function remove(card: EquipmentCard): Promise<void> {
    if (!window.confirm(`Удалить карточку «${card.title}»?`)) return
    try {
      await equipmentApi.remove(card.id)
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  async function bind(card: EquipmentCard, machineKey: string): Promise<void> {
    try {
      await equipmentApi.update(card.id, { machine_key: machineKey })
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  async function rebind(): Promise<void> {
    if (!window.confirm('Пересобрать автоматические привязки? Ручные останутся как есть.')) return
    try {
      setReport(await equipmentApi.rebind())
      setInfo('Привязки пересобраны')
      await load()
    } catch (err) {
      setError(message(err))
    }
  }

  const takenKeys = useMemo(
    () => new Set(cards.filter((card) => card.machine_key).map((card) => card.machine_key)),
    [cards],
  )

  const visible = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return cards
    return cards.filter((card) =>
      [card.inv_number, card.name, card.model, card.mdm_code, card.display_name, card.cnc_system]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(query)),
    )
  }, [cards, search])

  const guessCount = cards.filter((card) => card.binding_kind === 'guess').length

  return (
    <>
      <Alert onClose={() => setError('')}>{error}</Alert>
      <Alert kind="success" onClose={() => setInfo('')}>
        {info}
      </Alert>

      <Card
        title="Импорт таблицы оборудования"
        subtitle="выгрузка станков из мониторинга (колонки ID, Инвентарный_номер, Код_MDM) — по ID станки привязываются точно; таблица с «Инв.№» и «ID MDM» тоже подойдёт"
        actions={
          <button type="button" className="btn ghost" onClick={() => void rebind()}>
            Пересобрать привязки
          </button>
        }
      >
        <div
          className="dropzone"
          onDragOver={(event) => event.preventDefault()}
          onDrop={(event) => {
            event.preventDefault()
            void upload(event.dataTransfer.files?.[0])
          }}
          onClick={() => inputRef.current?.click()}
        >
          <input
            ref={inputRef}
            type="file"
            accept=".xlsx,.xlsm"
            hidden
            onChange={(event) => void upload(event.target.files?.[0])}
          />
          <strong>Перетащите файл .xlsx сюда</strong>
          <span>карточки обновляются по инвентарному номеру</span>
        </div>

        <ImportSummary report={report} />

        {guessCount > 0 && (
          <p className="hint">
            У {guessCount} карточек привязка расставлена по порядку — в загруженной таблице не было
            колонки <b>ID</b> с идентификатором станка, а код MDM общий для одинаковых моделей.
            Проверьте их в колонке «Станок» или загрузите выгрузку станков из мониторинга, где ID есть.
          </p>
        )}
      </Card>

      <Card
        title="Карточки оборудования"
        className="wide"
        actions={
          <div className="card-actions-row">
            <input
              className="search"
              placeholder="поиск по номеру, модели, коду…"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
            <button type="button" className="btn ghost" onClick={() => setCreating(true)}>
              Добавить
            </button>
          </div>
        }
      >
        {loading ? (
          <Loader />
        ) : cards.length === 0 ? (
          <Empty>
            Пока ничего нет — загрузите таблицу оборудования или добавьте карточку вручную
          </Empty>
        ) : (
          <div className="table-wrap tall">
            <table className="table">
              <thead>
                <tr>
                  <th>Инв. №</th>
                  <th>Наименование</th>
                  <th>Модель</th>
                  <th>ЧПУ</th>
                  <th>IP</th>
                  <th>Цех</th>
                  <th>Код MDM</th>
                  <th>Станок в мониторинге</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {visible.map((card) => (
                  <tr key={card.id}>
                    <td className="mono">{card.inv_number}</td>
                    <td className="ellipsis" title={card.name || ''}>
                      {card.display_name || card.name || '—'}
                    </td>
                    <td>{card.model || '—'}</td>
                    <td className="muted">{card.cnc_system || card.cnc_type || '—'}</td>
                    <td className="mono muted">{card.ip_address || '—'}</td>
                    <td className="num">{card.workshop || card.shop_number || '—'}</td>
                    <td className="mono muted">{card.mdm_code || '—'}</td>
                    <td>
                      <select
                        className={card.binding_kind === 'guess' ? 'guess' : ''}
                        value={card.machine_key || ''}
                        onChange={(event) => void bind(card, event.target.value)}
                      >
                        <option value="">— не привязан —</option>
                        {machines
                          .filter(
                            (machine) =>
                              machine.machine_key === card.machine_key ||
                              !takenKeys.has(machine.machine_key),
                          )
                          .map((machine) => (
                            <option key={machine.machine_key} value={machine.machine_key}>
                              {machine.machine_key}
                            </option>
                          ))}
                      </select>
                      <div className="sub">привязка: {BINDING_LABEL[card.binding_kind]}</div>
                    </td>
                    <td className="row-actions">
                      {card.machine_key && (
                        <Link
                          className="btn link"
                          to={`/machine/${encodeURIComponent(card.machine_key)}`}
                        >
                          страница
                        </Link>
                      )}
                      <button type="button" className="btn link" onClick={() => setEditing(card)}>
                        править
                      </button>
                      <button
                        type="button"
                        className="btn link danger"
                        onClick={() => void remove(card)}
                      >
                        удалить
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {editing && (
        <EquipmentDialog
          card={editing}
          onCancel={() => setEditing(null)}
          onSave={(values) => void save(editing, values)}
        />
      )}
      {creating && (
        <EquipmentDialog
          card={{}}
          isNew
          onCancel={() => setCreating(false)}
          onSave={(values) => void create(values)}
        />
      )}
    </>
  )
}
