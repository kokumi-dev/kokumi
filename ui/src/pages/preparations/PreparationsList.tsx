import { Link } from 'react-router'
import type { Preparation } from '../../api/types'
import { usePreparations } from '../../hooks/usePreparations'
import { matchesFilter, useFilterParam } from '../../hooks/useFilterParam'
import PageHeader from '../../components/layout/PageHeader'
import Section from '../../components/layout/Section'
import DataTable from '../../components/layout/DataTable'
import EmptyState from '../../components/layout/EmptyState'
import FilterInput from '../../components/layout/FilterInput'
import NameCell from '../../components/layout/NameCell'
import StatusIndicator from '../../components/shared/StatusIndicator'
import RelativeTime from '../../components/shared/RelativeTime'
import { paths } from '../../routes/paths'
import { shortDigest } from '../../utils/format'
import { approvalLabel } from '../../utils/preparations'
import layout from '../../components/layout/layout.module.css'

export default function PreparationsList() {
  const preparations = usePreparations()
  const [query, setQuery] = useFilterParam()

  const filtered = (preparations ?? [])
    .filter((p) => matchesFilter(query, p.name, p.namespace, p.order, p.commitMessage))
    .sort((a, b) => (b.createdAt ?? '').localeCompare(a.createdAt ?? ''))

  return (
    <div className={layout.page}>
      <PageHeader
        title="Preparations"
        subtitle="Immutable rendered manifests, one per Order revision"
      />

      <Section
        title="All Preparations"
        actions={<FilterInput label="Filter preparations" value={query} onChange={setQuery} placeholder="Filter by name, order or message…" />}
        flush
      >
        {preparations === null ? (
          <EmptyState text="Loading…" />
        ) : filtered.length === 0 ? (
          <EmptyState text={query ? 'No preparations match your filter' : 'No preparations yet'} />
        ) : (
          <DataTable<Preparation>
            rows={filtered}
            rowKey={(p) => `${p.namespace}/${p.name}`}
            rowHref={(p) => paths.preparation(p.namespace, p.name)}
            columns={[
              {
                header: 'Preparation',
                width: '30%',
                cell: (p) => (
                  <NameCell
                    to={paths.preparation(p.namespace, p.name)}
                    name={p.name}
                    secondary={p.commitMessage?.trim() || p.namespace}
                    label={p.isActive && <span className={layout.label}>Active</span>}
                  />
                ),
              },
              { header: 'Status', width: '110px', cell: (p) => <StatusIndicator state={p.state} /> },
              {
                header: 'Order',
                truncate: true,
                title: (p) => p.order,
                cell: (p) => <Link className={layout.refLink} to={paths.order(p.namespace, p.order)}>{p.order}</Link>,
              },
              {
                header: 'Approval',
                width: '150px',
                cell: (p) =>
                  p.approval
                    ? approvalLabel(p.approval.state, p.approval.approvedCount, p.approval.requiredApprovals)
                    : <span className={layout.muted}>—</span>,
              },
              {
                header: 'Digest',
                width: '110px',
                title: (p) => p.artifact.digest,
                truncate: true,
                cell: (p) => <span className={layout.mono}>{shortDigest(p.artifact.digest)}</span>,
              },
              { header: 'Created', width: '110px', cell: (p) => <RelativeTime iso={p.createdAt} /> },
            ]}
          />
        )}
      </Section>
    </div>
  )
}
