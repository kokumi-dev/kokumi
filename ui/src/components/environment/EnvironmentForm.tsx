import { useEffect, useState } from 'react'
import type { Environment, EnvironmentFormData, RequiredEnvironment } from '../../api/types'
import { emptyEnvironmentForm, environmentToFormData } from '../../api/types'
import { useEnvironments } from '../../hooks/useEnvironments'
import { soakSeconds, soakText } from '../../utils/format'
import { Button, CloseIcon, IconButton, PlusIcon } from '../ui'
import LabelSelectorEditor from '../shared/LabelSelectorEditor'
import ClusterPreview from '../cluster/ClusterPreview'
import styles from '../shared/Form.module.css'

interface Props {
  environment?: Environment
  /** The namespace of a new Environment. */
  initialNamespace?: string
  onCancel: () => void
  onSubmit: (data: EnvironmentFormData) => Promise<void>
  onDirtyChange?: (dirty: boolean) => void
}

/** Mirrors the MaxItems of the Environment's spec.promotion.requireAfter. */
const maxRequired = 8

/** Why the form cannot be saved yet; undefined when it can. */
function missing(form: EnvironmentFormData): string | undefined {
  if (!form.namespace.trim()) return 'Pick the namespace of the Environment'
  if (!form.name.trim()) return 'Name the Environment'
  if (form.requireAfter.some((r) => !r.environment)) return 'Pick an Environment in every required row'
  return undefined
}

/**
 * EnvironmentForm creates or changes an Environment: the Clusters of its
 * namespace it selects, previewed as they are labeled now, its Argo CD
 * project, and the Environments of its namespace an Order must run after
 * first. The Orders of its namespace deploy to it.
 */
export default function EnvironmentForm({ environment, initialNamespace, onCancel, onSubmit, onDirtyChange }: Props) {
  const [form, setForm] = useState<EnvironmentFormData>(
    environment ? environmentToFormData(environment) : emptyEnvironmentForm(initialNamespace),
  )
  const [initialForm] = useState(form)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const namespace = form.namespace.trim()

  const isEdit = Boolean(environment)
  const isDirty = JSON.stringify(form) !== JSON.stringify(initialForm)
  const blocked = missing(form)

  useEffect(() => {
    onDirtyChange?.(isDirty)
  }, [isDirty, onDirtyChange])

  function set<K extends keyof EnvironmentFormData>(key: K, value: EnvironmentFormData[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  async function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await onSubmit({
        ...form,
        namespace,
        name: form.name.trim(),
        description: form.description.trim(),
        project: form.project.trim() || 'default',
      })
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form id="environment-form" onSubmit={handleSubmit}>
      <div className={styles.formCard}>
        <div className={styles.formGrid}>
          <div className={styles.row2}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="environment-name">Name</label>
              <input
                id="environment-name"
                className={`${styles.input} ${isEdit ? styles.inputDisabled : ''}`}
                value={form.name}
                onChange={(e) => set('name', e.target.value)}
                placeholder="prod"
                readOnly={isEdit}
                required
              />
            </div>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="environment-namespace">Namespace</label>
              <input
                id="environment-namespace"
                className={`${styles.input} ${isEdit ? styles.inputDisabled : ''}`}
                value={form.namespace}
                onChange={(e) => set('namespace', e.target.value)}
                placeholder="kokumi"
                readOnly={isEdit}
                required
              />
              <span className={styles.hint}>The Orders of this namespace deploy to it, to Clusters of this namespace.</span>
            </div>
          </div>

          <div className={styles.row2}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="environment-description">Description (optional)</label>
              <input
                id="environment-description"
                className={styles.input}
                value={form.description}
                onChange={(e) => set('description', e.target.value)}
                placeholder="Production, in every region"
                maxLength={1024}
              />
            </div>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="environment-project">Argo CD project</label>
              <input
                id="environment-project"
                className={styles.input}
                value={form.project}
                onChange={(e) => set('project', e.target.value)}
                placeholder="default"
              />
              <span className={styles.hint}>
                The AppProject in namespace <code>argocd</code> of every Application that deploys here. Its destinations
                must admit the clusters.
              </span>
            </div>
          </div>

          <hr className={styles.divider} />
          <div className={styles.fieldGroup}>
            <span className={styles.label}>Clusters</span>
            <span className={styles.hint}>
              The Clusters of namespace <code>{namespace || '—'}</code> whose labels match; without labels or
              expressions, every one of them.
            </span>
            <LabelSelectorEditor
              value={form.clusterSelector}
              onChange={(selector) => set('clusterSelector', selector)}
              example={['stage', 'prod']}
            />
            {namespace && (
              <ClusterPreview
                namespace={namespace}
                selector={form.clusterSelector}
                emptyText="No Cluster matches: Orders that deploy here have nowhere to deploy."
              />
            )}
          </div>

          <hr className={styles.divider} />
          <RequiredEnvironmentsEditor
            namespace={namespace}
            self={form.name}
            value={form.requireAfter}
            onChange={(required) => set('requireAfter', required)}
          />

          {error && <div className={styles.fieldError}>{error}</div>}
        </div>
      </div>
      <div className={styles.formActions}>
        <Button variant="secondary" onClick={onCancel} disabled={submitting}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={submitting || (isEdit && !isDirty)} disabledReason={blocked}>
          {submitting ? 'Saving…' : isEdit ? 'Save changes' : 'Create Environment'}
        </Button>
      </div>
    </form>
  )
}

interface RequiredRow {
  environment: string
  /** The least soak in minutes, as typed. */
  minutes: string
}

interface RequiredEnvironmentsEditorProps {
  /** The namespace of the Environment, whose Environments it may require. */
  namespace: string
  /** The Environment's own name, which it cannot require. */
  self: string
  /** Seeds the rows once. */
  value: RequiredEnvironment[]
  onChange: (required: RequiredEnvironment[]) => void
}

/** The requirements of rows, with a soak only when one is typed. */
function requiredOf(rows: RequiredRow[]): RequiredEnvironment[] {
  return rows.map((r) => {
    const seconds = soakSeconds(r.minutes)
    return seconds ? { environment: r.environment, minSoakSeconds: seconds } : { environment: r.environment }
  })
}

/**
 * RequiredEnvironmentsEditor picks the Environments of the namespace an
 * Order must run after before it deploys here: it must name an Order of each
 * in its upstream stages, with at least the soak.
 */
function RequiredEnvironmentsEditor({ namespace, self, value, onChange }: RequiredEnvironmentsEditorProps) {
  const environments = useEnvironments()
  const [rows, setRows] = useState<RequiredRow[]>(() =>
    value.map((r) => ({ environment: r.environment, minutes: soakText(r.minSoakSeconds) })),
  )
  const known = (environments ?? []).filter((e) => e.namespace === namespace && e.name !== self.trim()).map((e) => e.name)

  function update(next: RequiredRow[]) {
    setRows(next)
    onChange(requiredOf(next))
  }

  return (
    <div className={styles.fieldGroup}>
      <span className={styles.label}>Requires</span>
      <span className={styles.hint}>
        Environments of the namespace an Order must run after before it deploys here: it names an Order of each in its
        upstream stages, with at least the soak, in minutes. Without any, Orders deploy here without upstream stages.
      </span>
      {rows.map((r, i) => {
        const taken = new Set(rows.filter((_, j) => j !== i).map((q) => q.environment))
        return (
          <div key={i} className={styles.setRow}>
            <select
              className={styles.input}
              style={{ flex: 1 }}
              aria-label="Required Environment"
              value={r.environment}
              onChange={(e) => update(rows.map((q, j) => (j === i ? { ...q, environment: e.target.value } : q)))}
            >
              <option value="">— select an Environment —</option>
              {r.environment && !known.includes(r.environment) && (
                <option value={r.environment}>{r.environment} (not found)</option>
              )}
              {known.filter((n) => !taken.has(n)).map((n) => (
                <option key={n} value={n}>{n}</option>
              ))}
            </select>
            <input
              className={styles.input}
              style={{ flex: 1 }}
              type="number"
              min={0}
              step="any"
              aria-label={`Least soak after ${r.environment || 'the Environment'} (minutes)`}
              value={r.minutes}
              onChange={(e) => update(rows.map((q, j) => (j === i ? { ...q, minutes: e.target.value } : q)))}
              placeholder="Least soak (minutes), none by default"
            />
            <IconButton tone="danger" onClick={() => update(rows.filter((_, j) => j !== i))} aria-label="Remove required Environment">
              <CloseIcon />
            </IconButton>
          </div>
        )
      })}
      <div>
        <Button
          variant="ghost"
          size="sm"
          icon={<PlusIcon />}
          onClick={() => update([...rows, { environment: '', minutes: '' }])}
          disabled={rows.length >= maxRequired}
        >
          Require an Environment
        </Button>
      </div>
    </div>
  )
}
