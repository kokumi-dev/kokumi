import { useState } from 'react'
import type { LabelSelector, LabelSelectorOperator, LabelSelectorRequirement } from '../../api/types'
import { labelSelectorOperators } from '../../api/types'
import { Button, CloseIcon, IconButton, PlusIcon } from '../ui'
import LabelsEditor from './LabelsEditor'
import styles from './Form.module.css'
import own from './LabelSelectorEditor.module.css'

/** An expression as typed: its values comma-separated. */
interface ExpressionRow {
  key: string
  operator: LabelSelectorOperator
  values: string
}

interface LabelSelectorEditorProps {
  /** Seeds the editor once; key it to reset it. */
  value?: LabelSelector
  onChange: (selector: LabelSelector) => void
  /** A label of the rows' placeholders, e.g. ['region', 'eu']. */
  example?: [string, string]
}

/** Whether an operator compares the label with values. */
const takesValues = (operator: LabelSelectorOperator) => operator === 'In' || operator === 'NotIn'

/** The requirement of a row; rows without a key are left out. */
function requirementOf(row: ExpressionRow): LabelSelectorRequirement {
  const values = [...new Set(row.values.split(',').map((v) => v.trim()).filter(Boolean))]
  return {
    key: row.key.trim(),
    operator: row.operator,
    ...(takesValues(row.operator) && values.length > 0 ? { values } : {}),
  }
}

/** The selector of the labels and expression rows, leaving out what is empty, so that nothing set is {}. */
function selectorOf(labels: Record<string, string>, rows: ExpressionRow[]): LabelSelector {
  const expressions = rows.filter((r) => r.key.trim()).map(requirementOf)
  return {
    ...(Object.keys(labels).length > 0 ? { matchLabels: labels } : {}),
    ...(expressions.length > 0 ? { matchExpressions: expressions } : {}),
  }
}

/**
 * LabelSelectorEditor edits a Kubernetes label selector: labels that must
 * match exactly, and expressions such as region In (eu, us). A selector
 * without either selects everything.
 */
export default function LabelSelectorEditor({ value, onChange, example = ['region', 'eu'] }: LabelSelectorEditorProps) {
  const [labels, setLabels] = useState<Record<string, string>>(() => value?.matchLabels ?? {})
  const [rows, setRows] = useState<ExpressionRow[]>(() =>
    (value?.matchExpressions ?? []).map((e) => ({ key: e.key, operator: e.operator, values: (e.values ?? []).join(', ') })),
  )

  function setExpressions(next: ExpressionRow[]) {
    setRows(next)
    onChange(selectorOf(labels, next))
  }

  function updateRow(index: number, change: Partial<ExpressionRow>) {
    setExpressions(rows.map((r, i) => (i === index ? { ...r, ...change } : r)))
  }

  return (
    <div className={own.editor}>
      <div className={styles.fieldGroup}>
        <span className={own.title}>Labels</span>
        <LabelsEditor
          value={labels}
          onChange={(next) => {
            setLabels(next)
            onChange(selectorOf(next, rows))
          }}
          keyPlaceholder={example[0]}
          valuePlaceholder={example[1]}
        />
      </div>

      <div className={styles.fieldGroup}>
        <span className={own.title}>Expressions</span>
        {rows.map((r, i) => (
          <div key={i} className={styles.setRow}>
            <input
              className={styles.setKey}
              aria-label="Expression key"
              value={r.key}
              onChange={(e) => updateRow(i, { key: e.target.value })}
              placeholder={example[0]}
            />
            <select
              className={own.operator}
              aria-label={`Operator of ${r.key || 'the expression'}`}
              value={r.operator}
              onChange={(e) => updateRow(i, { operator: e.target.value as LabelSelectorOperator })}
            >
              {labelSelectorOperators.map((o) => (
                <option key={o} value={o}>{o}</option>
              ))}
            </select>
            <input
              className={styles.setValue}
              aria-label={`Values of ${r.key || 'the expression'}`}
              value={takesValues(r.operator) ? r.values : ''}
              disabled={!takesValues(r.operator)}
              onChange={(e) => updateRow(i, { values: e.target.value })}
              placeholder={takesValues(r.operator) ? `${example[1]}, …` : 'No values'}
            />
            <IconButton tone="danger" onClick={() => setExpressions(rows.filter((_, j) => j !== i))} aria-label="Remove expression">
              <CloseIcon />
            </IconButton>
          </div>
        ))}
        <div>
          <Button
            variant="ghost"
            size="sm"
            icon={<PlusIcon />}
            onClick={() => setRows([...rows, { key: '', operator: 'In', values: '' }])}
          >
            Add expression
          </Button>
        </div>
      </div>
    </div>
  )
}
