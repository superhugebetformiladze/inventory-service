/**
 * Вся «начинка» дашборда: какие фильтры выбраны и какие данные под них загружены.
 *
 * Хук (функция, имя которой начинается с use) — это способ вынести логику из
 * компонента, оставив в нём только разметку. Здесь он:
 *   * хранит выбранные фильтры;
 *   * подтягивает список выгрузок и справочник станков;
 *   * при каждом изменении фильтров заново запрашивает четыре отчёта сразу;
 *   * список записей грузит отдельно — он нужен, только если открыли эту вкладку.
 */
import { useCallback, useEffect, useMemo, useState } from 'react'

import { dataApi, filesApi } from '@/api'
import { toInputDate } from '@/lib/format'
import type {
  DataQuery,
  DayRow,
  Heatmap,
  MachineOption,
  MachineRow,
  Overview,
  RecordsPage,
  SourceFile,
} from '@/types/api'

export type Scope = 'file' | 'range'

export interface DashboardState {
  files: SourceFile[]
  machines: MachineOption[]
  availableShifts: number[]
  params: DataQuery
  overview: Overview | null
  machineRows: MachineRow[]
  timeline: DayRow[]
  heatmap: Heatmap | null
  records: RecordsPage | null
  loading: boolean
  error: string
  setError: (value: string) => void
  loadRecords: () => void
}

export interface DashboardFilters {
  scope: Scope
  setScope: (value: Scope) => void
  fileId: number | null
  setFileId: (value: number) => void
  scheduleType: number
  setScheduleType: (value: number) => void
  shift: string
  setShift: (value: string) => void
  dateFrom: string
  setDateFrom: (value: string) => void
  dateTo: string
  setDateTo: (value: string) => void
  selectedMachines: string[]
  setSelectedMachines: (value: string[]) => void
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function useDashboardData(): DashboardState & { filters: DashboardFilters } {
  const [files, setFiles] = useState<SourceFile[]>([])
  const [scope, setScope] = useState<Scope>('file')
  const [fileId, setFileId] = useState<number | null>(null)
  const [scheduleType, setScheduleType] = useState(1)
  const [shift, setShift] = useState('')
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')
  const [machines, setMachines] = useState<MachineOption[]>([])
  const [selectedMachines, setSelectedMachines] = useState<string[]>([])
  const [availableShifts, setAvailableShifts] = useState<number[]>([])

  const [overview, setOverview] = useState<Overview | null>(null)
  const [machineRows, setMachineRows] = useState<MachineRow[]>([])
  const [timeline, setTimeline] = useState<DayRow[]>([])
  const [heatmap, setHeatmap] = useState<Heatmap | null>(null)
  const [records, setRecords] = useState<RecordsPage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    filesApi
      .list()
      .then((rows) => {
        const ready = rows.filter((row) => row.status === 'ok')
        setFiles(ready)
        if (ready.length) {
          setFileId((current) => current ?? ready[0].id)
          setDateFrom(toInputDate(ready[0].date_min))
          setDateTo(toInputDate(ready[0].date_max))
        } else {
          setLoading(false)
        }
      })
      .catch((err) => {
        setError(message(err))
        setLoading(false)
      })
  }, [])

  useEffect(() => {
    dataApi
      .filters(scope === 'file' ? fileId : undefined)
      .then((data) => {
        setMachines(data.machines)
        setAvailableShifts(data.shifts)
        setSelectedMachines([])
        if (scope === 'range') {
          setDateFrom((current) => current || toInputDate(data.date_min))
          setDateTo((current) => current || toInputDate(data.date_max))
        }
      })
      .catch((err) => setError(message(err)))
  }, [scope, fileId])

  const params = useMemo<DataQuery>(() => {
    const base: DataQuery = {
      scope,
      schedule_type: scheduleType,
      shift: shift || undefined,
      machines: selectedMachines.length ? selectedMachines : undefined,
    }
    if (scope === 'file') return { ...base, file_id: fileId }
    return {
      ...base,
      date_from: dateFrom ? `${dateFrom}T00:00:00` : undefined,
      date_to: dateTo ? `${dateTo}T00:00:00` : undefined,
    }
  }, [scope, fileId, scheduleType, shift, selectedMachines, dateFrom, dateTo])

  const load = useCallback(async () => {
    if (scope === 'file' && !fileId) return
    setLoading(true)
    setError('')
    try {
      const [overviewData, machinesData, timelineData, heatmapData] = await Promise.all([
        dataApi.overview(params),
        dataApi.machines(params),
        dataApi.timeline(params),
        dataApi.heatmap(params),
      ])
      setOverview(overviewData)
      setMachineRows(machinesData)
      setTimeline(timelineData)
      setHeatmap(heatmapData)
      setRecords(null)
    } catch (err) {
      setError(message(err))
    } finally {
      setLoading(false)
    }
  }, [params, scope, fileId])

  useEffect(() => {
    void load()
  }, [load])

  const loadRecords = useCallback(() => {
    if (records) return
    dataApi
      .records({ ...params, limit: 300 })
      .then(setRecords)
      .catch((err) => setError(message(err)))
  }, [params, records])

  return {
    files,
    machines,
    availableShifts,
    params,
    overview,
    machineRows,
    timeline,
    heatmap,
    records,
    loading,
    error,
    setError,
    loadRecords,
    filters: {
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
    },
  }
}
