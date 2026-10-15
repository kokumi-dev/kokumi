import type { Patch } from '../../api/types'
import { Button, CloseIcon, IconButton, PlusIcon } from '../ui'
import styles from './Form.module.css'

interface Props {
  patches: Patch[]
  onChange: (patches: Patch[]) => void
}

/** Edits a list of patches, each a target resource plus JSONPath → value pairs. */
export default function PatchesEditor({ patches, onChange }: Props) {
  return (
    <>
      <div className={styles.patchList}>
        {patches.map((patch, idx) => (
          <PatchEditor
            key={idx}
            index={idx}
            patch={patch}
            onUpdate={(p) => onChange(patches.map((q, i) => (i === idx ? p : q)))}
            onRemove={() => onChange(patches.filter((_, i) => i !== idx))}
          />
        ))}
      </div>
      <Button
        block
        className={styles.addPatch}
        icon={<PlusIcon />}
        onClick={() => onChange([...patches, { target: { kind: '', name: '' }, set: {} }])}
      >
        Add patch
      </Button>
    </>
  )
}

interface PatchEditorProps {
  index: number
  patch: Patch
  onUpdate: (p: Patch) => void
  onRemove: () => void
}

function PatchEditor({ index, patch, onUpdate, onRemove }: PatchEditorProps) {
  const setEntries = Object.entries(patch.set)

  function updateTarget(field: keyof Patch['target'], val: string) {
    onUpdate({ ...patch, target: { ...patch.target, [field]: val } })
  }

  function addSetEntry() {
    onUpdate({ ...patch, set: { ...patch.set, '': '' } })
  }

  function updateSetEntry(oldKey: string, newKey: string, val: string) {
    const next: Record<string, string> = {}
    for (const [k, v] of Object.entries(patch.set)) {
      if (k === oldKey) {
        next[newKey] = val
      } else {
        next[k] = v
      }
    }
    onUpdate({ ...patch, set: next })
  }

  function removeSetEntry(key: string) {
    const next = { ...patch.set }
    delete next[key]
    onUpdate({ ...patch, set: next })
  }

  return (
    <div className={styles.patchCard}>
      <div className={styles.patchCardHeader}>
        <span className={styles.patchCardTitle}>Patch {index + 1}</span>
        <IconButton tone="danger" onClick={onRemove} aria-label="Remove patch">
          <CloseIcon />
        </IconButton>
      </div>

      <TargetFields target={patch.target} onChange={(field, val) => updateTarget(field, val)} />

      <div>
        <label className={styles.label}>Set (JSONPath → value)</label>
        {setEntries.map(([k, v], i) => (
          <div key={i} className={styles.setRow}>
            <input
              className={styles.setKey}
              value={k}
              onChange={(e) => updateSetEntry(k, e.target.value, v)}
              placeholder=".spec.replicas"
            />
            <input
              className={styles.setValue}
              value={v}
              onChange={(e) => updateSetEntry(k, k, e.target.value)}
              placeholder="3"
            />
            <IconButton tone="danger" onClick={() => removeSetEntry(k)} aria-label="Remove key/value">
              <CloseIcon />
            </IconButton>
          </div>
        ))}
        <Button variant="ghost" size="sm" icon={<PlusIcon />} onClick={addSetEntry}>
          Add key/value
        </Button>
      </div>
    </div>
  )
}

interface TargetFieldsProps {
  target: Patch['target']
  onChange: (field: keyof Patch['target'], val: string) => void
  /** What an empty namespace means. */
  namespacePlaceholder?: string
}

/** Kind, name and optional namespace of a patch target. */
export function TargetFields({ target, onChange, namespacePlaceholder = 'inherit from Order namespace' }: TargetFieldsProps) {
  return (
    <>
      <div className={styles.row2}>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Kind</label>
          <input
            className={styles.input}
            value={target.kind}
            onChange={(e) => onChange('kind', e.target.value)}
            placeholder="Deployment"
          />
        </div>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Name</label>
          <input
            className={styles.input}
            value={target.name}
            onChange={(e) => onChange('name', e.target.value)}
            placeholder="my-app"
          />
        </div>
      </div>

      <div className={styles.fieldGroup}>
        <label className={styles.label}>Namespace (optional)</label>
        <input
          className={styles.input}
          value={target.namespace ?? ''}
          onChange={(e) => onChange('namespace', e.target.value)}
          placeholder={namespacePlaceholder}
        />
      </div>
    </>
  )
}
