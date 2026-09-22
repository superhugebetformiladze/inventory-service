/** Выбор станков для фильтра: кнопка со списком-раскладушкой и галочками. */
import { useState } from 'react'

import type { MachineOption } from '@/types/api'

interface MachinePickerProps {
  machines: MachineOption[]
  selected: string[]
  onChange: (value: string[]) => void
}

export function MachinePicker({ machines, selected, onChange }: MachinePickerProps) {
  const [open, setOpen] = useState(false)
  const label = selected.length ? `Выбрано: ${selected.length}` : 'Все станки'

  function toggle(key: string) {
    onChange(selected.includes(key) ? selected.filter((item) => item !== key) : [...selected, key])
  }

  return (
    <div className="machine-picker">
      <button type="button" className="btn ghost" onClick={() => setOpen((value) => !value)}>
        Станки: {label} {open ? '▲' : '▼'}
      </button>
      {selected.length > 0 && (
        <button type="button" className="btn link" onClick={() => onChange([])}>
          сбросить
        </button>
      )}
      {open && (
        <div className="machine-list">
          {machines.map((machine) => (
            <label key={machine.key} className="check">
              <input
                type="checkbox"
                checked={selected.includes(machine.key)}
                onChange={() => toggle(machine.key)}
              />
              <span>{machine.name}</span>
            </label>
          ))}
        </div>
      )}
    </div>
  )
}
