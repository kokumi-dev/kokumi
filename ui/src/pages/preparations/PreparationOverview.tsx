import { Link } from 'react-router'
import Section from '../../components/layout/Section'
import PropertyList from '../../components/layout/PropertyList'
import ConditionList from '../../components/layout/ConditionList'
import { paths } from '../../routes/paths'
import { formatDate } from '../../utils/format'
import { approvalLabel } from '../../utils/preparations'
import { usePreparationContext } from './preparationContext'
import layout from '../../components/layout/layout.module.css'

export default function PreparationOverview() {
  const { prep, parent } = usePreparationContext()
  const git = prep.gitSource
  const signed = prep.conditions?.find((c) => c.type === 'Signed')

  return (
    <>
      <Section title="Details">
        <PropertyList
          items={[
            {
              label: 'Order',
              value: <Link className={layout.inlineLink} to={paths.order(prep.namespace, prep.order)}>{prep.order}</Link>,
            },
            { label: 'Created', value: formatDate(prep.createdAt) },
            { label: 'Commit message', value: prep.commitMessage?.trim(), show: !!prep.commitMessage?.trim() },
            {
              label: 'Parent',
              value: parent ? (
                <Link className={`${layout.mono} ${layout.inlineLink}`} to={paths.preparation(parent.namespace, parent.name)}>
                  {parent.name}
                </Link>
              ) : (
                <span className={layout.mono}>{prep.parentDigest}</span>
              ),
              show: !!prep.parentDigest,
            },
            {
              label: 'Approval',
              value: prep.approval
                ? approvalLabel(prep.approval.state, prep.approval.approvedCount, prep.approval.requiredApprovals)
                : 'Not required',
            },
          ]}
        />
      </Section>

      <Section
        title="Artifact"
        description="Immutable OCI artifact this Preparation was rendered into"
        actions={<Link className={layout.inlineLink} to={paths.preparation(prep.namespace, prep.name, 'artifact')}>View structure</Link>}
      >
        <PropertyList
          items={[
            { label: 'Reference', value: <span className={layout.mono}>{prep.artifact.ociRef}</span> },
            { label: 'Digest', value: <span className={layout.mono}>{prep.artifact.digest}</span> },
            {
              label: 'Registry',
              value: prep.artifact.registryRef
                ? <Link className={layout.inlineLink} to={paths.registry(prep.namespace, prep.artifact.registryRef)}>{prep.artifact.registryRef}</Link>
                : <span className={layout.muted}>None: pushed without credentials</span>,
            },
            { label: 'Config hash', value: <span className={layout.mono}>{prep.configHash}</span> },
            {
              label: 'Signed',
              value: signed?.status === 'True' ? 'Yes' : signed ? `No: ${signed.message ?? signed.reason}` : 'Not yet',
            },
          ]}
        />
      </Section>

      <Section title="Renderer" description="What rendered this Preparation, as reported by the controller">
        <PropertyList
          items={[
            { label: 'Type', value: prep.renderer.type },
            { label: 'Version', value: prep.renderer.version },
            { label: 'Builder', value: <span className={layout.mono}>{prep.renderer.builder}</span> },
          ]}
        />
      </Section>

      {git && (
        <Section title="Source">
          <PropertyList
            items={[
              { label: 'Repository', value: <span className={layout.mono}>{git.repo}</span>, show: !!git.repo },
              { label: 'Tag', value: git.tag, show: !!git.tag },
              { label: 'Commit', value: <span className={layout.mono}>{git.commitHash}</span>, show: !!git.commitHash },
              {
                label: 'Link',
                value: git.sourceLink && (
                  <a className={layout.inlineLink} href={git.sourceLink.url} target="_blank" rel="noreferrer noopener">
                    {git.sourceLink.label} ↗
                  </a>
                ),
                show: !!git.sourceLink,
              },
            ]}
          />
        </Section>
      )}

      <Section title="Conditions">
        <ConditionList conditions={prep.conditions} />
      </Section>
    </>
  )
}
