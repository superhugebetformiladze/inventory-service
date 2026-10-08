/**
 * Админка: файлы, оборудование, продукция и пользователи.
 *
 * Открытый раздел записан в адресе страницы (?tab=equipment), поэтому на него можно
 * дать ссылку коллеге, и он не сбрасывается при обновлении страницы.
 */
import { useSearchParams } from 'react-router-dom'

import { EquipmentPanel } from './EquipmentPanel'
import { FilesPanel } from './FilesPanel'
import { ProductionPanel } from './production/ProductionPanel'
import { UsersPanel } from './UsersPanel'

type Tab = 'files' | 'equipment' | 'production' | 'users'

const TABS: [Tab, string][] = [
  ['files', 'Файлы'],
  ['equipment', 'Оборудование'],
  ['production', 'Продукция'],
  ['users', 'Пользователи'],
]

export function AdminPage() {
  // Вкладка живёт в адресе: на неё можно дать ссылку, и она переживает обновление страницы.
  const [search, setSearch] = useSearchParams()
  const requested = search.get('tab') as Tab | null
  const tab: Tab = requested && TABS.some(([key]) => key === requested) ? requested : 'files'

  const setTab = (value: Tab) =>
    setSearch((current) => {
      const next = new URLSearchParams(current)
      next.set('tab', value)
      return next
    })

  return (
    <div className="admin">
      <div className="tabs big">
        {TABS.map(([key, label]) => (
          <button
            key={key}
            type="button"
            className={tab === key ? 'active' : ''}
            onClick={() => setTab(key)}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === 'files' && <FilesPanel />}
      {tab === 'equipment' && <EquipmentPanel />}
      {tab === 'production' && <ProductionPanel />}
      {tab === 'users' && <UsersPanel />}
    </div>
  )
}
