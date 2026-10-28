import { useState } from 'react'
import { Button, CloseIcon, IconButton, PlusIcon } from '../ui'
import styles from './Form.module.css'

interface Row {
  key: string
  value: string
}

interface LabelsEditorProps {
  /** Seeds the rows once; key the editor to reset it. */
  value: Record<string, string>
  onChange: (labels: Record<string, string>) => void
  keyPlaceholder?: string
  valuePlaceholder?: string
}

/** The labels of rows: rows without a key are left out, and of a key set twice the last row counts. */
function labelsOf(rows: Row[]): Record<string, string> {
  return Object.fromEntries(rows.filter((r) => r.key.trim()).map((r) => [r.key.trim(), r.value.trim()]))
}

/**
 * LabelsEditor edits Kubernetes labels as key/value rows. It keeps rows that
 * are still being typed, without a key or with a key set twice, which the
 * labels it reports leave out.
 */
export default function LabelsEditor({
  value,
  onChange,
  keyPlaceholder = 'region',
  valuePlaceholder = 'eu',
}: LabelsEditorProps) {
  const [rows, setRows] = useState<Row[]>(() => Object.entries(value).map(([key, v]) => ({ key, value: v })))
  const keys = rows.map((r) => r.key.trim()).filter(Boolean)
  const twice = [...new Set(keys.filter((k, i) => keys.indexOf(k) !== i))]

  function update(next: Row[]) {
    setRows(next)
    onChange(labelsOf(next))
  }

  return (
    <div className={styles.fieldGroup}>
      {rows.map((r, i) => (
        <div key={i} className={styles.setRow}>
          <input
            className={styles.setKey}
            aria-label="Label key"
            value={r.key}
            onChange={(e) => update(rows.map((q, j) => (j === i ? { ...q, key: e.target.value } : q)))}
            placeholder={keyPlaceholder}
          />
          <input
            className={styles.setValue}
            aria-label={`Value of ${r.key || 'the label'}`}
            value={r.value}
            onChange={(e) => update(rows.map((q, j) => (j === i ? { ...q, value: e.target.value } : q)))}
            placeholder={valuePlaceholder}
          />
          <IconButton tone="danger" onClick={() => update(rows.filter((_, j) => j !== i))} aria-label="Remove label">
            <CloseIcon />
          </IconButton>
        </div>
      ))}
      {twice.length > 0 && (
        <span className={styles.fieldError}>
          {twice.join(', ')} {twice.length === 1 ? 'is' : 'are'} set twice; the last row counts.
        </span>
      )}
      <div>
        <Button variant="ghost" size="sm" icon={<PlusIcon />} onClick={() => setRows([...rows, { key: '', value: '' }])}>
          Add label
        </Button>
      </div>
    </div>
  )
}
