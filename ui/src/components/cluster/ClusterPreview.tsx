import { useEffect, useState } from 'react'
import type { Cluster, LabelSelector } from '../../api/types'
import { listClusters } from '../../api/client'
import { useClusters } from '../../hooks/useEnvironments'
import { paths } from '../../routes/paths'
import { clusterTone } from '../../utils/clusters'
import { plural } from '../../utils/format'
import { selectorString } from '../../utils/selectors'
import DataTable from '../layout/DataTable'
import NameCell from '../layout/NameCell'
import LabelList from '../shared/LabelList'
import StatusIndicator from '../shared/StatusIndicator'
import styles from '../shared/Form.module.css'

/** How long the selector must stay unchanged before the server is asked. */
const debounceMs = 300

interface Props {
  namespace: string
  selector: LabelSelector
  /** What it means when nothing is selected. */
  emptyText: string
}

/** The Clusters the server selected for a selector, or why it refused it. */
interface Selection {
  namespace: string
  clusters?: Cluster[]
  error?: string
}

/**
 * ClusterPreview lists the Clusters of a namespace a selector selects as
 * they are labeled now. The server selects them as the Environment's
 * controller does, once the selector stops changing; until then the last
 * selection stays.
 */
export default function ClusterPreview({ namespace, selector, emptyText }: Props) {
  const all = useClusters()
  const query = selectorString(selector)
  const [selection, setSelection] = useState<Selection | null>(null)

  useEffect(() => {
    let cancelled = false
    const timer = setTimeout(() => {
      listClusters(namespace, query)
        .then((clusters) => !cancelled && setSelection({ namespace, clusters }))
        .catch((err: unknown) => !cancelled && setSelection({ namespace, error: err instanceof Error ? err.message : String(err) }))
    }, debounceMs)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [namespace, query])

  if (selection?.namespace !== namespace) return <span className={styles.hint}>Selecting the Clusters…</span>
  if (selection.error) return <span className={styles.fieldError}>{selection.error}</span>

  const selected = selection.clusters ?? []
  // Every Cluster of the namespace, for users who may list them everywhere.
  const total = all?.filter((c) => c.namespace === namespace).length

  return (
    <div className={styles.fieldGroup} aria-live="polite">
      <span className={styles.hint}>
        Selects {total === undefined ? plural(selected.length, 'Cluster') : `${selected.length} of ${plural(total, 'Cluster')}`} of
        namespace {namespace}
      </span>
      {selected.length === 0 ? (
        <span className={styles.hint}>{emptyText}</span>
      ) : (
        <DataTable<Cluster>
          rows={selected}
          rowKey={(c) => c.name}
          pageSize={20}
          columns={[
            {
              header: 'Cluster',
              width: '28%',
              cell: (c) => <NameCell to={paths.cluster(c.namespace, c.name)} name={c.name} secondary={c.description} />,
            },
            { header: 'State', width: '150px', cell: (c) => <StatusIndicator state={c.state} tone={clusterTone(c)} /> },
            { header: 'Labels', wrap: true, cell: (c) => <LabelList labels={c.labels} /> },
          ]}
        />
      )}
    </div>
  )
}
