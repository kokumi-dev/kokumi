import Section from '../../components/layout/Section'
import ManifestPanel from '../../components/preparation/ManifestPanel'
import { usePreparationContext } from './preparationContext'

export default function PreparationFiles() {
  const { prep, order, editsAllowed } = usePreparationContext()

  return (
    <Section
      title="Rendered manifest"
      description={editsAllowed ? 'Edits are saved to the Order and produce a new Preparation.' : undefined}
    >
      <ManifestPanel key={prep.name} preparation={prep} order={editsAllowed ? order : undefined} />
    </Section>
  )
}
