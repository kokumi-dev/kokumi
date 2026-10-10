import Section from '../../components/layout/Section'
import PantryContents from '../../components/pantry/PantryContents'
import { usePantryContext } from './pantryContext'

export default function PantryTags() {
  const { pantry } = usePantryContext()

  return (
    <Section title="Tags" description="Artifacts available in this registry repository">
      <PantryContents key={`${pantry.namespace}/${pantry.name}`} pantry={pantry} />
    </Section>
  )
}
