import Section from '../../components/layout/Section'
import RepositoryContents from '../../components/repository/RepositoryContents'
import { useRepositoryContext } from './repositoryContext'

export default function RepositoryTags() {
  const { repository } = useRepositoryContext()

  return (
    <Section title="Tags" description="Artifacts available in this repository">
      <RepositoryContents key={`${repository.namespace}/${repository.name}`} repository={repository} />
    </Section>
  )
}
