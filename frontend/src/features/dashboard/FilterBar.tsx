/**
 * Панель фильтров над отчётами.
 *
 * Здесь выбирают, что именно смотреть: одну выгрузку или диапазон дат, режим учёта
 * времени (по графику смен или по календарным суткам), смену и конкретные станки.
 * Любое изменение сразу пересчитывает все отчёты на странице.
 */
import { dataApi } from '@/api'
import { formatDate, formatNumber } from '@/lib/format'
import type { DataQuery, MachineOption, SourceFile } from '@/types/api'

import { MachinePicker } from './MachinePicker'
import type { DashboardFilters } from './useDashboardData'

interface FilterBarProps {
  files: SourceFile[]
  machines: MachineOption[]
  availableShifts: number[]
  filters: DashboardFilters
  params: DataQuery
}

const SCHEDULE_TYPES = [
  { value: 1, label: 'По графику смен' },
  { value: 2, label: 'Календарные сутки' },
]

/** Панель фильтров: источник данных, режим учёта времени, смена, станки. */
export function FilterBar({ files, machines, availableShifts, filters, params }: FilterBarProps) {
  const {
    scope,
    setScope,
    fileId,
    setFileId,
    scheduleType,
    setScheduleType,
    shift,
    setShift,
    dateFrom,
    setDateFrom,
    dateTo,
    setDateTo,
    selectedMachines,
    setSelectedMachines,
  } = filters

  const currentFile = files.find((row) => row.id === fileId)

  return (
    <section className="filters">
      <div className="filters-row">
        <div className="segmented">
          <button
            type="button"
            className={scope === 'file' ? 'active' : ''}
            onClick={() => setScope('file')}
          >
            По файлу
          </button>
          <button
            type="button"
            className={scope === 'range' ? 'active' : ''}
            onClick={() => setScope('range')}
          >
            По датам
          </button>
        </div>

        {scope === 'file' ? (
          <label className="field inline grow">
            <span>Выгрузка</span>
            <select
              value={fileId ?? ''}
              onChange={(event) => setFileId(Number(event.target.value))}
            >
              {files.map((file) => (
                <option key={file.id} value={file.id}>
                  {formatDate(file.monitoring_from)} — {formatDate(file.monitoring_to)} ·{' '}
                  {file.original_name}
                </option>
              ))}
            </select>
          </label>
        ) : (
          <div className="field-group grow">
            <label className="field inline">
              <span>С</span>
              <input
                type="date"
                value={dateFrom}
                onChange={(event) => setDateFrom(event.target.value)}
              />
            </label>
            <label className="field inline">
              <span>По</span>
              <input
                type="date"
                value={dateTo}
                onChange={(event) => setDateTo(event.target.value)}
              />
            </label>
          </div>
        )}

        <label className="field inline">
          <span>Учёт времени</span>
          <select
            value={scheduleType}
            onChange={(event) => setScheduleType(Number(event.target.value))}
          >
            {SCHEDULE_TYPES.map((type) => (
              <option key={type.value} value={type.value}>
                {type.label}
              </option>
            ))}
          </select>
        </label>

        <label className="field inline">
          <span>Смена</span>
          <select value={shift} onChange={(event) => setShift(event.target.value)}>
            <option value="">Все</option>
            {availableShifts.map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </label>

        <button type="button" className="btn ghost" onClick={() => void dataApi.downloadCsv(params)}>
          Выгрузить CSV
        </button>
      </div>

      <MachinePicker
        machines={machines}
        selected={selectedMachines}
        onChange={setSelectedMachines}
      />

      {scope === 'range' && (
        <p className="hint">
          В режиме «по датам» за каждые сутки берётся самая свежая выгрузка — пересечения
          периодов не задваиваются.
        </p>
      )}
      {scope === 'file' && currentFile && (
        <p className="hint">
          Файл сформирован {formatDate(currentFile.replication_dt)} · записей{' '}
          {formatNumber(currentFile.records_count)} · станков {currentFile.machines_count}
        </p>
      )}
    </section>
  )
}
