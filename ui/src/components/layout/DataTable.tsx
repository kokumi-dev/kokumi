import { Fragment, useId, useState, type MouseEvent, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { Button, Chevron, IconButton } from '../ui'
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
  /** Wrap long content onto more lines instead of cutting it. */
  wrap?: boolean
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
   * Only called for open rows. They fade in when the user opens the row.
   */
  render: (row: T) => ReactNode
  /**
   * Draws a line from the open toggle down to the child rows, for rows that
   * continue it as a tree on the --tree-* custom properties of the table.
   */
  connected?: boolean
}

/** How many rows a table shows at first, and adds per "Show more". */
const defaultPageSize = 100

interface Props<T> {
  rows: T[]
  columns: Column<T>[]
  rowKey: (row: T) => string
  /** Makes the whole row navigate to this path. */
  rowHref?: (row: T) => string
  /** Makes the whole row select it instead; a link in the row should select it too, for the keyboard. */
  onRowClick?: (row: T) => void
  /** Highlights the selected row. */
  isRowSelected?: (row: T) => boolean
  expand?: RowExpansion<T>
  /**
   * How many rows it shows at first, and adds per "Show more", so that a
   * list of thousands does not render all of them at once.
   */
  pageSize?: number
}

export default function DataTable<T>({ rows, columns, rowKey, rowHref, onRowClick, isRowSelected, expand, pageSize = defaultPageSize }: Props<T>) {
  const navigate = useNavigate()
  const regionId = useId()
  // Rows the user has just opened. Their open animations run only while they
  // are listed here, so React re-inserting a row's nodes when the rows change
  // order does not replay them.
  const [opening, setOpening] = useState<ReadonlySet<string>>(() => new Set())
  const [shown, setShown] = useState(pageSize)
  const visible = rows.length > shown ? rows.slice(0, shown) : rows
  const more = Math.min(pageSize, rows.length - visible.length)

  function setRowOpening(key: string, on: boolean) {
    setOpening((prev) => {
      if (prev.has(key) === on) return prev
      const next = new Set(prev)
      if (on) next.add(key)
      else next.delete(key)
      return next
    })
  }

  function handleRowClick(e: MouseEvent<HTMLTableRowElement>, row: T) {
    if (!rowHref && !onRowClick) return
    // Let links, buttons and text selection inside the row behave normally.
    if ((e.target as HTMLElement).closest('a, button, input, select, textarea')) return
    if (window.getSelection()?.toString()) return
    if (onRowClick) onRowClick(row)
    else navigate(rowHref!(row))
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
          {visible.map((row, index) => {
            const key = rowKey(row)
            const open = expand?.isOpen(row) ?? false
            const animate = open && opening.has(key)
            const id = `${regionId}-${index}`
            return (
              <Fragment key={key}>
                <tbody>
                  <tr
                    className={[
                      rowHref || onRowClick ? styles.rowLink : '',
                      open ? styles.rowOpen : '',
                      isRowSelected?.(row) ? styles.rowSelected : '',
                    ].filter(Boolean).join(' ') || undefined}
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
                          setRowOpening(key, !open)
                          expand.onToggle(row)
                        }}
                      >
                        <IconButton
                          className={[styles.toggle, open ? styles.toggleOpen : '', animate ? styles.toggleOpening : ''].filter(Boolean).join(' ')}
                          // The pulse outlasts the reveal; when it ends, the row stops animating.
                          onAnimationEnd={(e) => {
                            if (e.target === e.currentTarget) setRowOpening(key, false)
                          }}
                          aria-label={expand.toggleLabel(row, open)}
                          aria-expanded={open}
                          aria-controls={id}
                        >
                          <Chevron open={open} />
                        </IconButton>
                      </td>
                    )}
                    {columns.map((c, i) => (
                      <td key={i} className={[c.alignEnd ? styles.alignEnd : '', c.wrap ? styles.wrap : ''].filter(Boolean).join(' ') || undefined}>
                        {c.truncate ? (
                          <div className={styles.truncate} title={c.title?.(row)}>{c.cell(row)}</div>
                        ) : (
                          c.cell(row)
                        )}
                      </td>
                    ))}
                  </tr>
                </tbody>
                {expand && open && (
                  <tbody id={id} className={animate ? styles.revealing : undefined}>
                    {expand.render(row)}
                  </tbody>
                )}
              </Fragment>
            )
          })}
        </table>
      </div>
      {more > 0 && (
        <div className={styles.tableMore}>
          <span className={styles.muted}>Showing {visible.length} of {rows.length}</span>
          <Button variant="ghost" size="sm" onClick={() => setShown((n) => n + pageSize)}>Show {more} more</Button>
        </div>
      )}
    </div>
  )
}
