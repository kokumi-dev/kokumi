import { useState } from 'react'
import type { Pantry, PantryFormData } from '../../api/types'
import { emptyPantryForm, pantryToFormData } from '../../api/types'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
import styles from './PantryFormModal.module.css'

interface Props {
  pantry?: Pantry
  onClose: () => void
  onSubmit: (data: PantryFormData) => Promise<void>
}

export default function PantryFormModal({ pantry, onClose, onSubmit }: Props) {
  const [form, setForm] = useState<PantryFormData>(
    pantry ? pantryToFormData(pantry) : emptyPantryForm(),
  )
  const [credMode, setCredMode] = useState<'direct' | 'secretRef'>(
    pantry?.secretRef ? 'secretRef' : 'direct',
  )
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const isEdit = Boolean(pantry)

  function set<K extends keyof PantryFormData>(key: K, value: PantryFormData[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function switchCredMode(mode: 'direct' | 'secretRef') {
    setCredMode(mode)
    if (mode === 'direct') {
      setForm((prev) => ({ ...prev, secretRef: '', credentialMode: 'direct' }))
    } else {
      setForm((prev) => ({ ...prev, username: '', password: '', credentialMode: 'secretRef' }))
    }
  }

  async function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await onSubmit({ ...form, credentialMode: credMode })
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal
      title={isEdit ? `Edit Pantry — ${pantry!.name}` : 'Add Pantry'}
      onClose={onClose}
      footer={
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Btn variant="secondary" size="sm" onClick={onClose} disabled={submitting}>Cancel</Btn>
          <Btn variant="primary" size="sm" onClick={() => { /* submit via form */ }} disabled={submitting} type="submit" form="pantry-form">
            {submitting ? 'Saving…' : isEdit ? 'Save' : 'Create'}
          </Btn>
        </div>
      }
    >
      <form id="pantry-form" onSubmit={handleSubmit}>
        <div className={styles.formGrid}>
          {/* Namespace — read-only in edit mode */}
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="pantry-namespace">Namespace</label>
            <input
              id="pantry-namespace"
              className={styles.input}
              value={form.namespace}
              onChange={(e) => set('namespace', e.target.value)}
              placeholder="kokumi"
              disabled={isEdit}
              required
            />
          </div>

          {/* Name — read-only in edit mode */}
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="pantry-name">Name</label>
            <input
              id="pantry-name"
              className={styles.input}
              value={form.name}
              onChange={(e) => set('name', e.target.value)}
              placeholder="my-registry"
              disabled={isEdit}
              required
            />
          </div>

          {/* URL */}
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="pantry-url">URL</label>
            <input
              id="pantry-url"
              className={styles.input}
              value={form.url}
              onChange={(e) => set('url', e.target.value)}
              placeholder="oci://ghcr.io/my-org/charts/myapp"
              required
            />
            <span className={styles.hint}>Full OCI URL including path (must start with <code>oci://</code>)</span>
          </div>

          {/* Description */}
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="pantry-description">Description (optional)</label>
            <input
              id="pantry-description"
              className={styles.input}
              value={form.description ?? ''}
              onChange={(e) => set('description', e.target.value)}
              placeholder="Internal Helm chart registry"
            />
          </div>

          {/* Credentials — mode switcher */}
          <hr className={styles.divider} />
          <div className={styles.sectionLabel}>Credentials (optional)</div>

          <div className={styles.credTabs}>
            <button
              type="button"
              className={`${styles.credTab} ${credMode === 'direct' ? styles.credTabActive : ''}`}
              onClick={() => switchCredMode('direct')}
            >
              Direct Credentials
            </button>
            <button
              type="button"
              className={`${styles.credTab} ${credMode === 'secretRef' ? styles.credTabActive : ''}`}
              onClick={() => switchCredMode('secretRef')}
            >
              Secret Ref
            </button>
          </div>

          {credMode === 'direct' ? (
            <>
              <div className={styles.row2}>
                <div className={styles.fieldGroup}>
                  <label className={styles.label} htmlFor="pantry-user">Username</label>
                  <input
                    id="pantry-user"
                    className={styles.input}
                    value={form.username ?? ''}
                    onChange={(e) => set('username', e.target.value)}
                    autoComplete="username"
                    placeholder="robot$kokumi"
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label className={styles.label} htmlFor="pantry-pass">Password / Token</label>
                  <input
                    id="pantry-pass"
                    type="password"
                    className={styles.input}
                    value={form.password ?? ''}
                    onChange={(e) => set('password', e.target.value)}
                    autoComplete="new-password"
                    placeholder="••••••••"
                  />
                </div>
              </div>
              {isEdit && (
                <span className={styles.hint}>Leave username/password blank to keep existing credentials.</span>
              )}
            </>
          ) : (
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="pantry-secretref">Secret Name</label>
              <input
                id="pantry-secretref"
                className={styles.input}
                value={form.secretRef ?? ''}
                onChange={(e) => set('secretRef', e.target.value)}
                placeholder="ghcr-creds"
              />
              <span className={styles.hint}>
                Name of an existing <code>kubernetes.io/dockerconfigjson</code> Secret in the same namespace.
              </span>
            </div>
          )}

          {error && (
            <div style={{ color: '#c62828', fontSize: '0.82rem' }}>{error}</div>
          )}
        </div>
      </form>
    </Modal>
  )
}
