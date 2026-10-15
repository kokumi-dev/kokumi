import { Fragment, useId, type MouseEvent, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { Chevron, IconButton } from '../ui'
import styles from './layout.module.css'

export interface Column<T> {
  header: ReactNode
  cell: (row: T) => ReactNode
  /** CSS width, e.g. '120px' or '20%'. Columns without a width share the rest. */
  width?: string
  /** Cut overflowing content with an ellipsis; `title` supplies the full value on hover. */
  truncate?: boolean
  title?: (row: T) => string | undefined
  /** Right-align header and cells, e.g. for a trailing action column. */
  alignEnd?: boolean
}

/**
 * Lets rows open child rows directly below them. The table adds a leading
 * chevron column to toggle them; the open state belongs to the caller.
 */
export interface RowExpansion<T> {
  isOpen: (row: T) => boolean
  onToggle: (row: T) => void
  /** Accessible name of the toggle, e.g. "Show Orders of podinfo". */
  toggleLabel: (row: T, open: boolean) => string
  /**
   * Child rows of an open row: <tr> elements with a leading cell under the
   * toggle column, then one cell per column. They share the table's columns
   * and headers, so assistive technology reads them like any other row.
   * Only called for open rows.
   */
  render: (row: T) => ReactNode
  /**
   * Draws a line from the open toggle down to the child rows, for rows that
   * continue it as a tree on the --tree-* custom properties of the table.
   */
  connected?: boolean
}

interface Props<T> {
  rows: T[]
  columns: Column<T>[]
  rowKey: (row: T) => string
  /** Makes the whole row navigate to this path. */
  rowHref?: (row: T) => string
  expand?: RowExpansion<T>
}

export default function DataTable<T>({ rows, columns, rowKey, rowHref, expand }: Props<T>) {
  const navigate = useNavigate()
  const regionId = useId()

  function handleRowClick(e: MouseEvent<HTMLTableRowElement>, row: T) {
    if (!rowHref) return
    // Let links, buttons and text selection inside the row behave normally.
    if ((e.target as HTMLElement).closest('a, button, input, select, textarea')) return
    if (window.getSelection()?.toString()) return
    navigate(rowHref(row))
  }

  return (
    <div className={styles.tableContainer}>
      <div className={styles.tableScroll}>
        <table className={styles.table}>
          <colgroup>
            {expand && <col style={{ width: '40px' }} />}
            {columns.map((c, i) => <col key={i} style={c.width ? { width: c.width } : undefined} />)}
          </colgroup>
          <thead>
            <tr>
              {expand && <th><span className={styles.visuallyHidden}>Expand</span></th>}
              {columns.map((c, i) => <th key={i} className={c.alignEnd ? styles.alignEnd : undefined}>{c.header}</th>)}
            </tr>
          </thead>
          {/* Each row has its own body, so the rows it opens form the next one. */}
          {rows.map((row, index) => {
            const open = expand?.isOpen(row) ?? false
            const id = `${regionId}-${index}`
            return (
              <Fragment key={rowKey(row)}>
                <tbody>
                  <tr
                    className={[rowHref ? styles.rowLink : '', open ? styles.rowOpen : ''].filter(Boolean).join(' ') || undefined}
                    onClick={(e) => handleRowClick(e, row)}
                  >
                    {expand && (
                      // The whole cell toggles, so a click that misses the button
                      // does not open the row's page. The button stays the
                      // keyboard target; its clicks bubble up to this handler.
                      <td
                        className={`${styles.toggleCell} ${open && expand.connected ? styles.toggleConnected : ''}`}
                        onClick={(e) => {
                          e.stopPropagation()
                          expand.onToggle(row)
                        }}
                      >
                        <IconButton
                          className={`${styles.toggle} ${open ? styles.toggleOpen : ''}`}
                          aria-label={expand.toggleLabel(row, open)}
                          aria-expanded={open}
                          aria-controls={id}
                        >
                          <Chevron open={open} />
                        </IconButton>
                      </td>
                    )}
                    {columns.map((c, i) => (
                      <td key={i} className={c.alignEnd ? styles.alignEnd : undefined}>
                        {c.truncate ? (
                          <div className={styles.truncate} title={c.title?.(row)}>{c.cell(row)}</div>
                        ) : (
                          c.cell(row)
                        )}
                      </td>
                    ))}
                  </tr>
                </tbody>
                {expand && open && <tbody id={id}>{expand.render(row)}</tbody>}
              </Fragment>
            )
          })}
        </table>
      </div>
    </div>
  )
}
