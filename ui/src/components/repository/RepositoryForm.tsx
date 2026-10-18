import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import type { Repository, RepositoryFormData } from '../../api/types'
import { emptyRepositoryForm, repositoryToFormData } from '../../api/types'
import { useRegistries } from '../../hooks/useRegistries'
import { matchRegistry } from '../../utils/registries'
import { stripOCIScheme } from '../../utils/format'
import { paths } from '../../routes/paths'
import { Button } from '../ui'
import styles from '../shared/Form.module.css'

interface Props {
  repository?: Repository
  onCancel: () => void
  onSubmit: (data: RepositoryFormData) => Promise<void>
  onDirtyChange?: (dirty: boolean) => void
}

export default function RepositoryForm({ repository, onCancel, onSubmit, onDirtyChange }: Props) {
  const [form, setForm] = useState<RepositoryFormData>(repository ? repositoryToFormData(repository) : emptyRepositoryForm())
  const [initialForm] = useState(form)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const registries = useRegistries()

  const isEdit = Boolean(repository)
  const isDirty = JSON.stringify(form) !== JSON.stringify(initialForm)
  const available = (registries ?? []).filter((r) => r.namespace === form.namespace)
  const selected = available.find((r) => r.name === form.registryRef)
  const notCovered = !!selected && !!form.url && !matchRegistry(form.url, [selected])

  useEffect(() => {
    onDirtyChange?.(isDirty)
  }, [isDirty, onDirtyChange])

  function set<K extends keyof RepositoryFormData>(key: K, value: RepositoryFormData[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  async function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await onSubmit(form)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form id="repository-form" onSubmit={handleSubmit}>
      <div className={styles.formCard}>
        <div className={styles.formGrid}>
          <div className={styles.row2}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="repository-namespace">Namespace</label>
              <input
                id="repository-namespace"
                className={styles.input}
                value={form.namespace}
                onChange={(e) => set('namespace', e.target.value)}
                placeholder="kokumi"
                disabled={isEdit}
                required
              />
            </div>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="repository-name">Name</label>
              <input
                id="repository-name"
                className={styles.input}
                value={form.name}
                onChange={(e) => set('name', e.target.value)}
                placeholder="podinfo"
                disabled={isEdit}
                required
              />
            </div>
          </div>

          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="repository-url">URL</label>
            <input
              id="repository-url"
              className={styles.input}
              value={form.url}
              onChange={(e) => set('url', e.target.value)}
              placeholder="oci://ghcr.io/my-org/charts/myapp"
              required
            />
            <span className={styles.hint}>The OCI repository, without a tag (must start with <code>oci://</code>)</span>
          </div>

          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="repository-registry">Registry</label>
            <select
              id="repository-registry"
              className={styles.input}
              value={form.registryRef}
              onChange={(e) => set('registryRef', e.target.value)}
            >
              <option value="">None: read anonymously</option>
              {available.map((r) => (
                <option key={r.name} value={r.name}>{r.name} ({stripOCIScheme(r.url)})</option>
              ))}
              {form.registryRef && !selected && <option value={form.registryRef}>{form.registryRef} (not found)</option>}
            </select>
            {notCovered ? (
              <span className={styles.fieldError}>The URL of Registry {selected.name} does not cover this repository.</span>
            ) : (
              <span className={styles.hint}>
                The UI browses the repository through this Registry, with your own access to its Secret. Products created
                from the repository list it. <Link to={paths.newRegistry()}>New Registry</Link>
              </span>
            )}
          </div>

          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="repository-description">Description (optional)</label>
            <input
              id="repository-description"
              className={styles.input}
              value={form.description}
              onChange={(e) => set('description', e.target.value)}
              placeholder="Internal Helm chart"
            />
          </div>

          {error && <div className={styles.fieldError}>{error}</div>}
        </div>
      </div>
      <div className={styles.formActions}>
        <Button variant="secondary" onClick={onCancel} disabled={submitting}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={submitting || (isEdit && !isDirty) || notCovered}>
          {submitting ? 'Saving…' : isEdit ? 'Save changes' : 'Create Repository'}
        </Button>
      </div>
    </form>
  )
}
