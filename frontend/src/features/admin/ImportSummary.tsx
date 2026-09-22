/** Отчёт после импорта Excel: что создано, что привязано и что осталось без пары. */
import type { ImportReport } from '@/types/api'

export function ImportSummary({ report }: { report: ImportReport | null }) {
  if (!report) return null

  return (
    <div className="import-report">
      <div>
        Листы: <b>{report.sheets.join(', ')}</b> · создано <b>{report.created}</b> · обновлено{' '}
        <b>{report.updated}</b>
      </div>
      <div>
        Привязано к станкам: точно по ID <b>{report.bound_exact}</b>
        {report.bound_guess > 0 && (
          <>
            {' '}
            , по порядку <b>{report.bound_guess}</b>
          </>
        )}
      </div>
      {report.unknown_machines.length > 0 && (
        <div className="muted">
          Указаны в файле, но не встречаются в выгрузках мониторинга:{' '}
          {report.unknown_machines.join(', ')}
        </div>
      )}
      {report.cards_without_machine.length > 0 && (
        <div className="muted">
          Без данных мониторинга: {report.cards_without_machine.join(', ')}
        </div>
      )}
      {report.machines_without_card.length > 0 && (
        <div className="muted">Станки без карточки: {report.machines_without_card.join(', ')}</div>
      )}
      {report.skipped.length > 0 && (
        <div className="sub error">Пропущено: {report.skipped.join('; ')}</div>
      )}
    </div>
  )
}
