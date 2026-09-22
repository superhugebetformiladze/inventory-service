/** Паспорт станка: поля карточки оборудования. Пустые поля не показываем. */
import { Empty } from '@/components/ui'
import { formatDate } from '@/lib/format'
import type { EquipmentCard } from '@/types/api'

const FIELDS: [keyof EquipmentCard, string][] = [
  ['name', 'Наименование'],
  ['model', 'Модель'],
  ['cnc_system', 'Система ЧПУ'],
  ['cnc_type', 'Тип УЧПУ'],
  ['cnc_name', 'Имя УЧПУ'],
  ['ip_address', 'IP-адрес'],
  ['inv_number', 'Инвентарный номер'],
  ['shop_number', 'Номер в цехе'],
  ['serial_number', 'Заводской номер'],
  ['manufacturer', 'Производитель'],
  ['year_made', 'Год выпуска'],
  ['workshop', 'Цех'],
  ['location', 'Расположение'],
  ['responsible', 'Ответственный'],
  ['commissioned_on', 'Ввод в эксплуатацию'],
  ['status', 'Состояние'],
  ['mdm_code', 'Код MDM'],
  ['notes', 'Примечание'],
  ['import_note', 'Примечание из таблицы'],
]

export function MachinePassport({ card }: { card: EquipmentCard | null }) {
  if (!card) {
    return (
      <Empty>
        Для этого станка нет карточки оборудования. Заведите её в разделе «Админка →
        Оборудование» или загрузите таблицу из Excel.
      </Empty>
    )
  }

  return (
    <dl className="passport">
      {FIELDS.filter(([key]) => card[key]).map(([key, label]) => (
        <div key={key}>
          <dt>{label}</dt>
          <dd>{key === 'commissioned_on' ? formatDate(card[key] as string) : String(card[key])}</dd>
        </div>
      ))}
    </dl>
  )
}
