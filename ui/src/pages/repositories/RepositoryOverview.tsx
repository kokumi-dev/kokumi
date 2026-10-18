import { Link } from 'react-router'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import { paths } from '../../routes/paths'
import { formatDate } from '../../utils/format'
import { useRepositoryContext } from './repositoryContext'
import layout from '../../components/layout/layout.module.css'

export default function RepositoryOverview() {
  const { repository } = useRepositoryContext()

  return (
    <>
      <Section title="Repository">
        <PropertyList
          items={[
            { label: 'URL', value: <span className={layout.mono}>{repository.url}</span> },
            {
              label: 'Registry',
              value: repository.registryRef
                ? <Link to={paths.registry(repository.namespace, repository.registryRef)}>{repository.registryRef}</Link>
                : <span className={layout.muted}>None: read anonymously</span>,
            },
            { label: 'Description', value: repository.description, show: !!repository.description },
            { label: 'Created', value: formatDate(repository.createdAt) },
          ]}
        />
      </Section>

      <Section title="Conditions">
        <ConditionList conditions={repository.conditions} />
      </Section>
    </>
  )
}
