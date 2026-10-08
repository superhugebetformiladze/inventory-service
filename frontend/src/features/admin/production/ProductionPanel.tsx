/**
 * Раздел админки «Продукция».
 *
 * Две части: «План и факт» — загрузка рапортов и строки выпуска за период,
 * «Номенклатура» — справочник изделий и закрепление их за станками.
 */
import { useState } from 'react'

import { ProductsSection } from './ProductsSection'
import { ReportsSection } from './ReportsSection'

type Section = 'plan' | 'products'

const SECTIONS: [Section, string][] = [
  ['plan', 'План и факт'],
  ['products', 'Номенклатура и станки'],
]

export function ProductionPanel() {
  const [section, setSection] = useState<Section>('plan')

  return (
    <>
      <div className="tabs">
        {SECTIONS.map(([key, label]) => (
          <button
            key={key}
            type="button"
            className={section === key ? 'active' : ''}
            onClick={() => setSection(key)}
          >
            {label}
          </button>
        ))}
      </div>

      {section === 'plan' ? <ReportsSection /> : <ProductsSection />}
    </>
  )
}
