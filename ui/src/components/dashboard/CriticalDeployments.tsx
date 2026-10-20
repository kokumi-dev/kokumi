import { Link } from 'react-router'
import type { Serving, VulnerabilitySummary } from '../../api/types'
import { useServings } from '../../hooks/useServings'
import { usePreparations } from '../../hooks/usePreparations'
import { paths } from '../../routes/paths'
import { summaryText } from '../../utils/vulnerabilities'
import Section from '../layout/Section'
import DataTable from '../layout/DataTable'
import NameCell from '../layout/NameCell'
import Badge from '../shared/Badge'
import layout from '../layout/layout.module.css'

interface Row {
  serving: Serving
  /** The deployed Preparation. */
  preparation: string
  /** Its source version, once the Preparations are loaded. */
  version?: string
  summary: VulnerabilitySummary
}

/**
 * The Servings whose deployed Preparation has critical findings, the most
 * first, each linking to that Preparation's Security tab. Renders nothing
 * while there are none.
 */
export default function CriticalDeployments() {
  const servings = useServings()
  const preparations = usePreparations()

  const rows: Row[] = (servings ?? [])
    .flatMap((serving) => {
      const summary = serving.deployedVulnerabilities
      const preparation = serving.deployedPreparation
      if (!summary || summary.critical === 0 || !preparation) return []
      const version = preparations?.find((p) => p.namespace === serving.namespace && p.name === preparation)?.version
      return [{ serving, preparation, version, summary }]
    })
    .sort((a, b) => b.summary.critical - a.summary.critical || b.summary.high - a.summary.high)

  if (rows.length === 0) return null

  const securityPath = (r: Row) => paths.preparation(r.serving.namespace, r.preparation, 'security')

  return (
    <Section title="Deployed with critical findings" description="Servings whose deployed Preparation has critical vulnerabilities" flush>
      <DataTable<Row>
        rows={rows}
        rowKey={(r) => `${r.serving.namespace}/${r.serving.name}`}
        rowHref={securityPath}
        columns={[
          {
            header: 'Serving',
            width: '30%',
            cell: (r) => (
              <NameCell to={paths.serving(r.serving.namespace, r.serving.name)} name={r.serving.name} secondary={r.serving.namespace} />
            ),
          },
          {
            header: 'Deployed',
            cell: (r) => (
              <>
                <Link className={layout.refLink} to={securityPath(r)} title={r.preparation}>
                  {r.version ?? r.preparation}
                </Link>
                {r.version && <span className={layout.secondaryText} title={r.preparation}>{r.preparation}</span>}
              </>
            ),
          },
          {
            header: 'Findings',
            width: '220px',
            cell: (r) => (
              <span className={layout.cellRow} title={summaryText(r.summary)}>
                <Badge state={`${r.summary.critical} critical`} tone="danger" />
                {r.summary.high > 0 && <Badge state={`${r.summary.high} high`} tone="warning" />}
              </span>
            ),
          },
        ]}
      />
    </Section>
  )
}
