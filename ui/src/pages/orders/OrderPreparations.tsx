import Section from '../../components/layout/Section'
import EmptyState from '../../components/layout/EmptyState'
import PreparationList from '../../components/preparation/PreparationList'
import { useOrderContext } from './orderContext'

export default function OrderPreparations() {
  const { order, preparations } = useOrderContext()

  return (
    <Section
      title="History"
      description={order.mode === 'Manual'
        ? 'Every render produces an immutable Preparation. Promote one to make it live.'
        : 'Every render produces an immutable Preparation. The latest Ready one is promoted automatically.'}
    >
      {preparations === null ? <EmptyState text="Loading…" /> : <PreparationList preparations={preparations} mode={order.mode} />}
    </Section>
  )
}
