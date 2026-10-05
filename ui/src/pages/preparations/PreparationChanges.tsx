import { Link, useSearchParams } from 'react-router'
import Section from '../../components/layout/Section'
import EmptyState from '../../components/layout/EmptyState'
import PreparationDiff from '../../components/preparation/PreparationDiff'
import { usePreparationContext } from './preparationContext'
import layout from '../../components/layout/layout.module.css'

type Base = 'parent' | 'active'

export default function PreparationChanges() {
  const { prep, parent, active } = usePreparationContext()
  const [search] = useSearchParams()

  const options: { id: Base; label: string; prep: typeof parent }[] = []
  if (parent) options.push({ id: 'parent', label: 'Previous Preparation', prep: parent })
  if (active && active.name !== prep.name && active.name !== parent?.name) {
    options.push({ id: 'active', label: 'Active Preparation', prep: active })
  }

  const requested = search.get('base') as Base | null
  const selected = options.find((o) => o.id === requested) ?? options[0]

  if (!selected?.prep) {
    return (
      <Section>
        <EmptyState
          text={prep.parentDigest
            ? 'The previous Preparation no longer exists, so there is nothing to compare against.'
            : 'This is the first Preparation of the Order, so there is nothing to compare against.'}
        />
      </Section>
    )
  }

  return (
    <Section
      title="Changes"
      description={`Compared with the ${selected.label.toLowerCase()}`}
      actions={options.length > 1 && (
        <div style={{ display: 'flex', gap: 12, fontSize: '0.82rem' }}>
          {options.map((o) =>
            o.id === selected.id ? (
              <strong key={o.id}>{o.label}</strong>
            ) : (
              <Link key={o.id} className={layout.inlineLink} to={`?base=${o.id}`} replace>{o.label}</Link>
            ),
          )}
        </div>
      )}
    >
      <PreparationDiff key={`${selected.prep.name}..${prep.name}`} base={selected.prep} target={prep} />
    </Section>
  )
}
