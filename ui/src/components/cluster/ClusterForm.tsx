import { useEffect, useRef, useState } from 'react'
import type { Cluster, ClusterCredentialMode, ClusterFormData } from '../../api/types'
import { clusterToFormData, emptyClusterForm } from '../../api/types'
import { useNamespaceCan } from '../../hooks/useCapability'
import { Button, SegmentedControl } from '../ui'
import LabelsEditor from '../shared/LabelsEditor'
import styles from '../shared/Form.module.css'

interface Props {
  cluster?: Cluster
  /** The namespace of a new Cluster. */
  initialNamespace: string
  onCancel: () => void
  onSubmit: (data: ClusterFormData) => Promise<void>
  onDirtyChange?: (dirty: boolean) => void
}

/** Why the form cannot be saved yet; undefined when it can. */
function missing(form: ClusterFormData): string | undefined {
  if (!form.namespace.trim()) return 'Pick the namespace of the Cluster'
  if (!form.name.trim()) return 'Name the Cluster'
  if (form.credentialMode === 'kubeconfig' && !form.kubeconfig.trim()) return 'Paste or upload a kubeconfig'
  if (form.credentialMode === 'token' && !(form.server.trim() && form.token.trim())) return 'Enter the server and a token'
  return undefined
}

/** Whether the form stores a kubeconfig in a Secret of the Cluster's namespace. */
const storesKubeconfig = (mode: ClusterCredentialMode) => mode === 'kubeconfig' || mode === 'token'

/**
 * ClusterForm creates or changes a Cluster: its description and labels,
 * which the Environments of its namespace select it by, and how Kokumi
 * reaches it: as the cluster it runs in, or with a self-contained kubeconfig
 * or a server with a token, stored in a Secret of the namespace. The
 * connection is set once; the kubeconfig can be replaced later.
 */
export default function ClusterForm({ cluster, initialNamespace, onCancel, onSubmit, onDirtyChange }: Props) {
  const [form, setForm] = useState<ClusterFormData>(cluster ? clusterToFormData(cluster) : emptyClusterForm(initialNamespace))
  const [initialForm] = useState(form)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  const isEdit = Boolean(cluster)
  const isDirty = JSON.stringify(form) !== JSON.stringify(initialForm)
  // The UI writes the kubeconfig Secret with the user's identity, and a
  // Cluster may only name a Secret its author may read: the permission is
  // granted per namespace.
  const canStoreKubeconfig = useNamespaceCan(form.namespace.trim(), 'clusters.credentials:update')
  const credentialsDenied = storesKubeconfig(form.credentialMode) && canStoreKubeconfig === false
  const blocked = missing(form) ?? (credentialsDenied ? 'You may not store Secrets in this namespace' : undefined)

  useEffect(() => {
    onDirtyChange?.(isDirty)
  }, [isDirty, onDirtyChange])

  function set<K extends keyof ClusterFormData>(key: K, value: ClusterFormData[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function switchCredential(mode: ClusterCredentialMode) {
    setForm((prev) => ({ ...prev, credentialMode: mode, kubeconfig: '', server: '', caData: '', token: '' }))
  }

  async function upload(file?: File) {
    if (!file) return
    try {
      set('kubeconfig', await file.text())
    } catch (err: unknown) {
      setError(`Could not read ${file.name}: ${err instanceof Error ? err.message : String(err)}`)
    }
  }

  async function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await onSubmit({ ...form, namespace: form.namespace.trim(), name: form.name.trim(), description: form.description.trim() })
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  const namespace = form.namespace.trim() || '<namespace>'
  // The Secret an existing Cluster names; the server names the Secret of a new one.
  const secret = cluster?.kubeconfigSecret
  const segments: { id: ClusterCredentialMode; label: string }[] = [
    isEdit ? { id: 'keep', label: 'Keep the stored one' } : { id: 'inCluster', label: 'In cluster' },
    { id: 'kubeconfig', label: 'Kubeconfig' },
    { id: 'token', label: 'Server and token' },
  ]

  return (
    <form id="cluster-form" onSubmit={handleSubmit}>
      <div className={styles.formCard}>
        <div className={styles.formGrid}>
          <div className={styles.row2}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="cluster-name">Name</label>
              <input
                id="cluster-name"
                className={`${styles.input} ${isEdit ? styles.inputDisabled : ''}`}
                value={form.name}
                onChange={(e) => set('name', e.target.value)}
                placeholder="prod-eu-1"
                readOnly={isEdit}
                required
              />
            </div>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="cluster-namespace">Namespace</label>
              <input
                id="cluster-namespace"
                className={`${styles.input} ${isEdit ? styles.inputDisabled : ''}`}
                value={form.namespace}
                onChange={(e) => set('namespace', e.target.value)}
                placeholder="platform"
                readOnly={isEdit}
                required
              />
              <span className={styles.hint}>The Environments of this namespace may select it.</span>
            </div>
          </div>

          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="cluster-description">Description (optional)</label>
            <input
              id="cluster-description"
              className={styles.input}
              value={form.description}
              onChange={(e) => set('description', e.target.value)}
              placeholder="Production in Frankfurt, run by the platform team"
              maxLength={1024}
            />
          </div>

          <div className={styles.fieldGroup}>
            <span className={styles.label}>Labels</span>
            <LabelsEditor value={form.labels} onChange={(labels) => set('labels', labels)} />
            <span className={styles.hint}>
              Environments select their Clusters by these labels, such as <code>stage=prod</code> or <code>region=eu</code>.
            </span>
          </div>

          <hr className={styles.divider} />
          <div className={styles.sectionTitle}>Connection</div>

          {cluster?.connection === 'InCluster' ? (
            <span className={styles.hint}>
              The cluster Kokumi runs in: Argo CD deploys to its own cluster, so there is no credential to change.
            </span>
          ) : (
            <>
              <SegmentedControl<ClusterCredentialMode>
                aria-label="Connection"
                segments={segments}
                value={form.credentialMode}
                onChange={switchCredential}
              />

              {form.credentialMode === 'inCluster' && (
                <span className={styles.hint}>
                  The cluster Kokumi runs in: Argo CD deploys to its own cluster, and Kokumi reads it with its own
                  ServiceAccount, so there is no credential to store. Of two Clusters of an Environment that reach the
                  same cluster, the later one by name is held. Kokumi keeps one in its own namespace.
                </span>
              )}

              {form.credentialMode === 'kubeconfig' && (
                <div className={styles.fieldGroup}>
                  <label className={styles.label} htmlFor="cluster-kubeconfig">Kubeconfig</label>
                  <textarea
                    id="cluster-kubeconfig"
                    className={styles.valuesArea}
                    style={{ minHeight: 180 }}
                    value={form.kubeconfig}
                    onChange={(e) => set('kubeconfig', e.target.value)}
                    placeholder={'apiVersion: v1\nkind: Config\nclusters: …'}
                    spellCheck={false}
                    autoComplete="off"
                  />
                  <div>
                    <input
                      ref={fileInput}
                      type="file"
                      hidden
                      onChange={(e) => {
                        void upload(e.target.files?.[0])
                        e.target.value = ''
                      }}
                    />
                    <Button variant="ghost" size="sm" onClick={() => fileInput.current?.click()}>Upload a file…</Button>
                  </div>
                  <span className={styles.hint}>
                    Self-contained: the current context&apos;s server with <code>certificate-authority-data</code>, and a{' '}
                    <code>token</code> or <code>client-certificate-data</code> and <code>client-key-data</code> inline.
                    Exec plugins, auth providers and file references are not supported.
                  </span>
                </div>
              )}

              {form.credentialMode === 'token' && (
                <>
                  <div className={styles.fieldGroup}>
                    <label className={styles.label} htmlFor="cluster-server">Server</label>
                    <input
                      id="cluster-server"
                      className={styles.input}
                      value={form.server}
                      onChange={(e) => set('server', e.target.value)}
                      placeholder="https://prod-eu-1.example.com:6443"
                    />
                  </div>
                  <div className={styles.fieldGroup}>
                    <label className={styles.label} htmlFor="cluster-ca">CA certificate (optional)</label>
                    <textarea
                      id="cluster-ca"
                      className={styles.valuesArea}
                      value={form.caData}
                      onChange={(e) => set('caData', e.target.value)}
                      placeholder={'-----BEGIN CERTIFICATE-----\n…'}
                      spellCheck={false}
                    />
                    <span className={styles.hint}>The PEM-encoded CA of the server; without it the system roots are trusted.</span>
                  </div>
                  <div className={styles.fieldGroup}>
                    <label className={styles.label} htmlFor="cluster-token">Token</label>
                    <input
                      id="cluster-token"
                      type="password"
                      className={styles.input}
                      value={form.token}
                      onChange={(e) => set('token', e.target.value)}
                      autoComplete="new-password"
                      placeholder="••••••••"
                    />
                    <span className={styles.hint}>
                      A bearer token of a ServiceAccount on the cluster that may manage what Argo CD deploys there.
                    </span>
                  </div>
                </>
              )}

              {form.credentialMode === 'keep' ? (
                <span className={styles.hint}>
                  The stored kubeconfig{secret && <> in Secret <code>{secret}</code></>} stays as it is.
                </span>
              ) : form.credentialMode !== 'inCluster' && (
                <span className={styles.hint}>
                  Stored as a kubeconfig in {secret ? <>the Secret <code>{secret}</code></> : 'a Secret'} of namespace{' '}
                  <code>{namespace}</code>, which is deleted with the Cluster; {isEdit ? 'replacing' : 'storing'} it
                  needs permission to {isEdit ? 'change' : 'create'} Secrets in that namespace. Kokumi registers the
                  cluster with Argo CD from it.
                </span>
              )}
            </>
          )}

          {credentialsDenied && (
            <div className={styles.fieldError}>
              You may not read or store Secrets in the namespace <code>{form.namespace.trim()}</code>, and a Cluster can only
              name Secrets its author may read. A platform admin grants this by binding the ClusterRole{' '}
              <code>kokumi-cluster-credentials</code> in that namespace; never in Kokumi&apos;s own namespace, where it would
              grant reading the server&apos;s Secrets.
            </div>
          )}

          {error && <div className={styles.fieldError}>{error}</div>}
        </div>
      </div>
      <div className={styles.formActions}>
        <Button variant="secondary" onClick={onCancel} disabled={submitting}>Cancel</Button>
        <Button
          type="submit"
          variant="primary"
          disabled={submitting || (isEdit && !isDirty)}
          disabledReason={blocked}
        >
          {submitting ? 'Saving…' : isEdit ? 'Save changes' : 'Create Cluster'}
        </Button>
      </div>
    </form>
  )
}
