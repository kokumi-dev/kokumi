import PageHeader from '../components/layout/PageHeader'
import Section from '../components/layout/Section'
import EmptyState from '../components/layout/EmptyState'
import { ButtonLink } from '../components/ui'
import { paths } from '../routes/paths'
import layout from '../components/layout/layout.module.css'

export default function NotFound() {
  return (
    <div className={layout.page}>
      <PageHeader title="Page not found" />
      <Section>
        <EmptyState
          text="There is nothing at this address."
          action={<ButtonLink variant="secondary" size="sm" to={paths.dashboard()}>Go to Dashboard</ButtonLink>}
        />
      </Section>
    </div>
  )
}
