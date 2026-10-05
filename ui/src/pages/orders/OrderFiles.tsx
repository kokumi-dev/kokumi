import { Link } from 'react-router'
import Section from '../../components/layout/Section'
import EmptyState from '../../components/layout/EmptyState'
import ManifestPanel from '../../components/preparation/ManifestPanel'
import { paths } from '../../routes/paths'
import { useOrderContext } from './orderContext'
import layout from '../../components/layout/layout.module.css'

export default function OrderFiles() {
  const { order, preparations, editsAllowed } = useOrderContext()

  const active = preparations?.find((p) => p.isActive)
  const prep = active ?? preparations?.[0]

  if (!prep) {
    return (
      <Section>
        <EmptyState text={preparations === null ? 'Loading…' : 'No Preparation has been rendered yet.'} />
      </Section>
    )
  }

  return (
    <Section
      title="Rendered manifest"
      description={
        <>
          From the {active ? 'active' : 'latest'} Preparation{' '}
          <Link className={`${layout.mono} ${layout.inlineLink}`} to={paths.preparation(prep.namespace, prep.name)}>
            {prep.name}
          </Link>
          {editsAllowed ? '. Edits are saved to the Order and produce a new Preparation.' : ''}
        </>
      }
    >
      <ManifestPanel key={prep.name} preparation={prep} order={editsAllowed ? order : undefined} />
    </Section>
  )
}
