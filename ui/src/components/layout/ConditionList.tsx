import type { Condition } from '../../api/types'
import Badge from '../shared/Badge'
import type { Tone } from '../shared/tone'
import styles from './layout.module.css'

/** How a condition's status reads. */
function statusBadge(status: string): { label: string; tone: Tone } {
  if (status === 'True') return { label: 'Ready', tone: 'success' }
  if (status === 'False') return { label: 'Failed', tone: 'danger' }
  return { label: 'Pending', tone: 'warning' }
}

export default function ConditionList({ conditions }: { conditions?: Condition[] }) {
  if (!conditions || conditions.length === 0) {
    return <span className={styles.muted}>No conditions reported.</span>
  }
  return (
    <div className={styles.conditionList}>
      {conditions.map((c) => {
        const badge = statusBadge(c.status)
        return (
          <div key={c.type} className={styles.condition}>
            <div className={styles.conditionHeader}>
              <span className={styles.conditionType}>{c.type}</span>
              <Badge state={badge.label} tone={badge.tone} />
              {c.reason && <span className={styles.conditionReason}>{c.reason}</span>}
            </div>
            {c.message && <span className={styles.conditionMessage}>{c.message}</span>}
          </div>
        )
      })}
    </div>
  )
}
