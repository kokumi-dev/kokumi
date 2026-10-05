import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import { formatDate } from '../../utils/format'
import { usePantryContext } from './pantryContext'
import layout from '../../components/layout/layout.module.css'

export default function PantryOverview() {
  const { pantry } = usePantryContext()

  return (
    <>
      <Section title="Connection">
        <PropertyList
          items={[
            { label: 'URL', value: <span className={layout.mono}>{pantry.url}</span> },
            { label: 'Credentials Secret', value: pantry.secretRef ?? <span className={layout.muted}>None</span> },
            { label: 'Description', value: pantry.description, show: !!pantry.description },
            { label: 'Created', value: formatDate(pantry.createdAt) },
          ]}
        />
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={pantry.conditions} />
      </Section>
    </>
  )
}
