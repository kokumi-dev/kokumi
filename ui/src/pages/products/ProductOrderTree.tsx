import type { MouseEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import type { Order, Product } from '../../api/types'
import StatusIndicator from '../../components/shared/StatusIndicator'
import RelativeTime from '../../components/shared/RelativeTime'
import { paths } from '../../routes/paths'
import { plural } from '../../utils/format'
import layout from '../../components/layout/layout.module.css'
import type { OrderInsight } from './orderInsights'
import styles from './ProductOrderTree.module.css'

/** Cells of a child row after its lead cell: one per Products table column. */
const COLUMNS = 6

interface Props {
  product: Product
  /** Null while the Orders are still loading. */
  insights: OrderInsight[] | null
  /** Why the user may not create Orders; undefined when allowed. */
  orderDenied?: string
}

/**
 * The Orders of an expanded Product as branches below it: rows of the
 * Products table, under its columns and headers. Each shows what the Order
 * runs and whether a newer Preparation waits; a last row adds an Order.
 */
export default function ProductOrderTree({ product, insights, orderDenied }: Props) {
  const navigate = useNavigate()

  if (insights === null) {
    return (
      <tr className={styles.row}>
        <td className={styles.lead} />
        <td colSpan={COLUMNS} className={styles.muted}>Loading Orders…</td>
      </tr>
    )
  }

  function openOrder(e: MouseEvent, to: string) {
    // Let links and text selection inside the row behave normally, and keep
    // the tree column below the toggle inert: the Order opens from its name on.
    const target = e.target as HTMLElement
    if (target.closest('a, button') || target.closest(`.${styles.lead}`)) return
    if (window.getSelection()?.toString()) return
    navigate(to)
  }

  const empty = insights.length === 0
  const addLabel = empty ? 'Create the first Order' : 'New Order'

  return (
    <>
      {insights.map((insight) => {
        const { order } = insight
        const to = paths.order(order.namespace, order.name)
        const detail = `${promotion(order)} · ${insight.overlay}`
        return (
          <tr key={order.name} className={`${styles.row} ${styles.orderRow}`} onClick={(e) => openOrder(e, to)}>
            <td className={styles.lead} />
            <td>
              <span className={styles.name}>
                <span className={`${styles.node} ${insight.active ? styles.nodeLive : ''}`} aria-hidden />
                <span className={styles.nameText}>
                  <span className={layout.cellRow}>
                    <span className={layout.visuallyHidden}>Order of {product.name}: </span>
                    <Link className={layout.primaryLink} to={to} title={order.name}>{order.name}</Link>
                  </span>
                  <span className={layout.secondaryText} title={detail}>{detail}</span>
                </span>
              </span>
            </td>
            <td><StatusIndicator state={order.state} /></td>
            <td>
              <span className={styles.running}>
                {insight.active ? (
                  <>
                    <span className={styles.label}>Runs</span>
                    <span className={styles.prep} title={`Runs ${insight.active}`}>{insight.active}</span>
                  </>
                ) : (
                  <span className={styles.muted}>Not deployed</span>
                )}
                {insight.updateReady && (
                  <span className={styles.update} title="A newer Preparation is waiting to be promoted">Update ready</span>
                )}
              </span>
            </td>
            <td><RunningVersion insight={insight} /></td>
            <td />
            <td><RelativeTime iso={order.createdAt} /></td>
          </tr>
        )
      })}
      <tr className={`${styles.row} ${styles.addRow}`}>
        <td className={styles.lead} />
        <td colSpan={COLUMNS}>
          {empty && <><span className={styles.empty}>No Orders yet</span>{' '}</>}
          {orderDenied ? (
            <span className={`${styles.add} ${styles.addDisabled}`} title={orderDenied}>{addLabel}</span>
          ) : (
            <Link to={paths.newOrder(product)} className={`${styles.add} ${empty ? styles.addFirst : ''}`}>{addLabel}</Link>
          )}
        </td>
      </tr>
    </>
  )
}

/** Version of what the Order runs; the Product row shows the declared one. */
function RunningVersion({ insight }: { insight: OrderInsight }) {
  if (insight.activeVersion) {
    return <span className={layout.mono} title="Version of the running Preparation">{insight.activeVersion}</span>
  }
  if (insight.versionLoading) {
    return (
      <span className={styles.pending}>
        <span className={layout.visuallyHidden}>Loading version</span>
      </span>
    )
  }
  return <span className={styles.muted}>—</span>
}

/** Promotion in words, e.g. "Manual · 2 approvals". */
function promotion(order: Order): string {
  return order.approvals ? `${order.mode} · ${plural(order.approvals.requiredApprovals, 'approval')}` : order.mode
}
