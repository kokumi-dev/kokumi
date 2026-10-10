import type { Condition } from '../../api/types'
import Badge from '../shared/Badge'
import styles from './layout.module.css'

export default function ConditionList({ conditions }: { conditions?: Condition[] }) {
  if (!conditions || conditions.length === 0) {
    return <span className={styles.muted}>No conditions reported.</span>
  }
  return (
    <div className={styles.conditionList}>
      {conditions.map((c) => (
        <div key={c.type} className={styles.condition}>
          <div className={styles.conditionHeader}>
            <span className={styles.conditionType}>{c.type}</span>
            <Badge state={c.status === 'True' ? 'Ready' : c.status === 'False' ? 'Failed' : 'Pending'} />
            {c.reason && <span className={styles.conditionReason}>{c.reason}</span>}
          </div>
          {c.message && <span className={styles.conditionMessage}>{c.message}</span>}
        </div>
      ))}
    </div>
  )
}
