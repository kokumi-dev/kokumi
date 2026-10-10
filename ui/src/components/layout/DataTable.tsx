import type { MouseEvent, ReactNode } from 'react'
import { useNavigate } from 'react-router'
import styles from './layout.module.css'

export interface Column<T> {
  header: ReactNode
  cell: (row: T) => ReactNode
  /** CSS width, e.g. '120px' or '20%'. Columns without a width share the rest. */
  width?: string
  /** Cut overflowing content with an ellipsis; `title` supplies the full value on hover. */
  truncate?: boolean
  title?: (row: T) => string | undefined
}

interface Props<T> {
  rows: T[]
  columns: Column<T>[]
  rowKey: (row: T) => string
  /** Makes the whole row navigate to this path. */
  rowHref?: (row: T) => string
}

export default function DataTable<T>({ rows, columns, rowKey, rowHref }: Props<T>) {
  const navigate = useNavigate()

  function handleRowClick(e: MouseEvent<HTMLTableRowElement>, row: T) {
    if (!rowHref) return
    // Let links, buttons and text selection inside the row behave normally.
    if ((e.target as HTMLElement).closest('a, button, input, select, textarea')) return
    if (window.getSelection()?.toString()) return
    navigate(rowHref(row))
  }

  return (
    <div className={styles.tableScroll}>
      <table className={styles.table}>
        <colgroup>
          {columns.map((c, i) => <col key={i} style={c.width ? { width: c.width } : undefined} />)}
        </colgroup>
        <thead>
          <tr>
            {columns.map((c, i) => <th key={i}>{c.header}</th>)}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={rowKey(row)}
              className={rowHref ? styles.rowLink : undefined}
              onClick={(e) => handleRowClick(e, row)}
            >
              {columns.map((c, i) => (
                <td key={i}>
                  {c.truncate ? (
                    <div className={styles.truncate} title={c.title?.(row)}>{c.cell(row)}</div>
                  ) : (
                    c.cell(row)
                  )}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
