import { useEffect, useState } from 'react'
import type { CredentialMode, Registry, RegistryFormData, RegistryScheme } from '../../api/types'
import { emptyRegistryForm, registryCredentialsSecret, registryToFormData } from '../../api/types'
import { Button, SegmentedControl } from '../ui'
import { useNamespaceCan } from '../../hooks/useCapability'
import styles from './RegistryForm.module.css'

interface Props {
  registry?: Registry
  onCancel: () => void
  onSubmit: (data: RegistryFormData) => Promise<void>
  onDirtyChange?: (dirty: boolean) => void
}

export default function RegistryForm({ registry, onCancel, onSubmit, onDirtyChange }: Props) {
  const [form, setForm] = useState<RegistryFormData>(registry ? registryToFormData(registry) : emptyRegistryForm())
  const [initialForm] = useState(form)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const isEdit = Boolean(registry)
  const isDirty = JSON.stringify(form) !== JSON.stringify(initialForm)
  // Credentials are a Secret in the Registry's namespace, written with the
  // user's identity, and naming a Secret requires reading it; the
  // permission is granted per namespace.
  const canUseCredentials = useNamespaceCan(form.namespace, 'registries.credentials:update')
  const namesSecret = form.credentialMode !== 'none' || !!form.certSecretRef
  const credentialsDenied = namesSecret && canUseCredentials === false
  const storedSecret = registryCredentialsSecret(form.name || '<name>')
  const keepsStored = !!registry && registry.secretRef === registryCredentialsSecret(registry.name)

  useEffect(() => {
    onDirtyChange?.(isDirty)
  }, [isDirty, onDirtyChange])

  function set<K extends keyof RegistryFormData>(key: K, value: RegistryFormData[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function switchCredentialMode(mode: CredentialMode) {
    setForm((prev) => ({ ...prev, credentialMode: mode, username: '', password: '', secretRef: mode === 'secretRef' ? prev.secretRef : '' }))
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

  const typedIncomplete = form.credentialMode === 'direct' && !!form.username !== !!form.password
  const typedMissing = form.credentialMode === 'direct' && !form.username && !form.password && !keepsStored

  return (
    <form id="registry-form" onSubmit={handleSubmit}>
      <div className={styles.formCard}>
        <div className={styles.formGrid}>
          <div className={styles.row2}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="registry-namespace">Namespace</label>
              <input
                id="registry-namespace"
                className={styles.input}
                value={form.namespace}
                onChange={(e) => set('namespace', e.target.value)}
                placeholder="kokumi"
                disabled={isEdit}
                required
              />
            </div>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="registry-name">Name</label>
              <input
                id="registry-name"
                className={styles.input}
                value={form.name}
                onChange={(e) => set('name', e.target.value)}
                placeholder="ghcr-my-org"
                disabled={isEdit}
                required
              />
            </div>
          </div>

          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="registry-url">URL</label>
            <input
              id="registry-url"
              className={styles.input}
              value={form.url}
              onChange={(e) => set('url', e.target.value)}
              placeholder="oci://ghcr.io/my-org"
              required
            />
            <span className={styles.hint}>
              A prefix: <code>oci://host[:port][/path]</code>. The Registry connects to every location below it whose owner
              lists it in <code>registryRefs</code>; the longest matching URL wins.
            </span>
          </div>

          <div className={styles.fieldGroup}>
            <span className={styles.label}>Scheme</span>
            <SegmentedControl<RegistryScheme>
              aria-label="Scheme"
              segments={[
                { id: 'HTTPS', label: 'HTTPS' },
                { id: 'HTTP', label: 'HTTP' },
              ]}
              value={form.scheme}
              onChange={(s) => set('scheme', s)}
            />
            {form.scheme === 'HTTP' && (
              <span className={styles.hint}>
                Plain HTTP sends credentials and content unencrypted; use it only for registries inside the cluster network.
              </span>
            )}
          </div>

          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="registry-cert">TLS Secret (optional)</label>
            <input
              id="registry-cert"
              className={styles.input}
              value={form.certSecretRef}
              onChange={(e) => set('certSecretRef', e.target.value)}
              placeholder="registry-ca"
            />
            <span className={styles.hint}>
              A Secret with <code>ca.crt</code> to trust besides the system roots, and <code>tls.crt</code> and{' '}
              <code>tls.key</code> for a client certificate.
            </span>
          </div>

          <hr className={styles.divider} />
          <div className={styles.sectionLabel}>Credentials</div>

          <SegmentedControl<CredentialMode>
            aria-label="Credentials"
            segments={[
              { id: 'none', label: 'Anonymous' },
              { id: 'direct', label: 'Username and password' },
              { id: 'secretRef', label: 'Existing Secret' },
            ]}
            value={form.credentialMode}
            onChange={switchCredentialMode}
          />

          {form.credentialMode === 'direct' && (
            <>
              <div className={styles.row2}>
                <div className={styles.fieldGroup}>
                  <label className={styles.label} htmlFor="registry-user">Username</label>
                  <input
                    id="registry-user"
                    className={styles.input}
                    value={form.username}
                    onChange={(e) => set('username', e.target.value)}
                    autoComplete="username"
                    placeholder="robot$kokumi"
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label className={styles.label} htmlFor="registry-pass">Password / Token</label>
                  <input
                    id="registry-pass"
                    type="password"
                    className={styles.input}
                    value={form.password}
                    onChange={(e) => set('password', e.target.value)}
                    autoComplete="new-password"
                    placeholder="••••••••"
                  />
                </div>
              </div>
              <span className={styles.hint}>
                Stored in the <code>kubernetes.io/dockerconfigjson</code> Secret <code>{storedSecret}</code>, which the Registry
                owns and which is deleted with it.
                {keepsStored ? ' Leave both blank to keep the stored credentials.' : ' Enter both to save the Registry.'}
              </span>
            </>
          )}

          {form.credentialMode === 'secretRef' && (
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="registry-secretref">Secret Name</label>
              <input
                id="registry-secretref"
                className={styles.input}
                value={form.secretRef}
                onChange={(e) => set('secretRef', e.target.value)}
                placeholder="ghcr-creds"
                required
              />
              <span className={styles.hint}>
                A <code>kubernetes.io/dockerconfigjson</code> Secret in the same namespace with an entry for the URL's host.
              </span>
            </div>
          )}

          {credentialsDenied && (
            <span className={styles.error}>
              You may not read or store Secrets in the namespace <code>{form.namespace}</code>, and a Registry can only name
              Secrets its author may read. A platform admin grants this by binding the ClusterRole{' '}
              <code>kokumi-registry-credentials</code> in that namespace.
            </span>
          )}

          {error && <div className={styles.error}>{error}</div>}
        </div>
      </div>
      <div className={styles.formActions}>
        <Button variant="secondary" onClick={onCancel} disabled={submitting}>Cancel</Button>
        <Button
          type="submit"
          variant="primary"
          disabled={submitting || (isEdit && !isDirty) || credentialsDenied || typedIncomplete || typedMissing}
        >
          {submitting ? 'Saving…' : isEdit ? 'Save changes' : 'Create Registry'}
        </Button>
      </div>
    </form>
  )
}
