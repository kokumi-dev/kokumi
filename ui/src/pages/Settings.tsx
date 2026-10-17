import { useEffect, useRef, useState } from 'react'
import { Navigate, useParams } from 'react-router'
import styles from './pages.module.css'
import layout from '../components/layout/layout.module.css'
import Toggle from '../components/shared/Toggle'
import { Button, TabNav } from '../components/ui'
import PageHeader from '../components/layout/PageHeader'
import { paths } from '../routes/paths'
import { useAppInfo } from '../appContext'
import type { Capability } from '../api/capabilities'
import {
  getSettings,
  getSigning,
  saveSettings,
  saveAuthSettings,
  saveImagesSettings,
  saveSigningSettings,
} from '../api/client'
import type { AuthSettings, ImageMirror, ImagesSettings, Settings, SigningInfo, SigningSettings, TrustedKey } from '../api/client'
import { settingsTabs, type SettingsTab } from '../routes/paths'

const tabs = settingsTabs
type Tab = SettingsTab

/** Capability a tab requires; tabs the user lacks it for are hidden. */
const tabCapability: Record<Tab, Capability> = {
  general: 'settings.general:update',
  authentication: 'settings.auth:update',
  signing: 'settings.signing:update',
}

const tabLabel: Record<Tab, string> = {
  general: 'General',
  authentication: 'Authentication',
  signing: 'Signing',
}

const maxClaimLength = 253

/** Mirrors the MaxItems of the Kitchen's spec.images.mirrors. */
const maxImageMirrors = 32

/**
 * Settings shows the operator configuration of the Kitchen singleton in three
 * tabs: General (Argo CD URL, image mirrors), Authentication (admin user, OIDC) and Signing
 * (public key, trusted keys, source verification). Each tab is only shown
 * with its capability; the server still enforces RBAC on every write.
 */
export default function Settings() {
  const params = useParams()
  const tab = params.tab as Tab
  const { capabilities } = useAppInfo()
  const permittedTabs = tabs.filter((t) => capabilities?.has(tabCapability[t]))
  const [settings, setSettings] = useState<Settings | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  // Bumped whenever a save response lands so the active tab remounts and
  // re-seeds from the CR state instead of holding stale local edits.
  const [settingsRev, setSettingsRev] = useState(0)

  function load() {
    setLoading(true)
    setLoadError(null)
    getSettings()
      .then(setSettings)
      .catch((e) => setLoadError((e as Error).message || 'Failed to load settings'))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    // Initial fetch on mount; setState calls happen in async callbacks, not
    // in the effect body itself.
    void Promise.resolve().then(load)
  }, [])

  function handleSaved(next: Settings) {
    setSettings((prev) => ({ ...(prev ?? {}), ...next }))
    setSettingsRev((rev) => rev + 1)
  }

  if (!permittedTabs.includes(tab)) {
    return <Navigate to={permittedTabs.length > 0 ? paths.settings(permittedTabs[0]) : paths.dashboard()} replace />
  }

  const header = <PageHeader title="Settings" subtitle="Operator configuration and preferences" />

  if (loadError) {
    return (
      <div className={layout.page}>
        {header}
        <p className={styles.fieldError}>Failed to load settings: {loadError}</p>
        <Button variant="secondary" onClick={load}>
          Retry
        </Button>
      </div>
    )
  }

  return (
    <div className={layout.page}>
      {header}

      <TabNav tabs={permittedTabs.map((t) => ({ to: paths.settings(t), label: tabLabel[t] }))} />

      {loading ? (
        <p className={styles.fieldHint}>Loading…</p>
      ) : tab === 'general' ? (
        <div className={styles.groupStack}>
          <GeneralTab key={`g-${settingsRev}`} argoCDURL={settings?.argoCDURL ?? ''} onSaved={handleSaved} />
          <ImageMirrorsSection key={`m-${settingsRev}`} images={settings?.images} onSaved={handleSaved} />
        </div>
      ) : tab === 'signing' ? (
        <SigningTab key={`s-${settingsRev}`} signing={settings?.signing} onSaved={handleSaved} />
      ) : (
        <AuthenticationTab key={`a-${settingsRev}`} auth={settings?.auth} onSaved={handleSaved} />
      )}
    </div>
  )
}

// ── General tab ──────────────────────────────────────────────────────────────

function GeneralTab({ argoCDURL, onSaved }: { argoCDURL: string; onSaved: (s: Settings) => void }) {
  const [base, setBase] = useState(argoCDURL)
  const [saved, setSaved] = useState(false)
  const [urlError, setUrlError] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Clear the saved-feedback timer on unmount (tab switch) to avoid a
  // state update on a dead component.
  useEffect(() => () => {
    if (savedTimer.current) clearTimeout(savedTimer.current)
  }, [])

  function isValidURL(val: string): boolean {
    if (!val) return true // empty = clear setting, that's fine
    try {
      const u = new URL(val)
      return u.protocol === 'http:' || u.protocol === 'https:'
    } catch {
      return false
    }
  }

  function handleSave() {
    const trimmed = base.trim().replace(/\/$/, '')
    if (!isValidURL(trimmed)) {
      setUrlError(true)
      return
    }
    setSubmitting(true)
    saveSettings(trimmed)
      .then((s) => {
        setBase(trimmed)
        setUrlError(false)
        setSaveError(null)
        setSaved(true)
        savedTimer.current = setTimeout(() => setSaved(false), 2000)
        onSaved(s)
      })
      .catch((e) => {
        setSaveError((e as Error).message || 'Failed to save settings')
      })
      .finally(() => setSubmitting(false))
  }

  return (
    <div className={styles.section}>
      <div className={styles.sectionHeader}>
        <span className={styles.sectionTitle}>Argo CD</span>
      </div>
      <div className={styles.sectionBody}>
        <div className={styles.settingsSection}>
          <div className={styles.fieldRow}>
            <label className={styles.fieldLabel} htmlFor="argoCDBase">
              Base URL
            </label>
            <input
              id="argoCDBase"
              className={urlError ? `${styles.fieldInput} ${styles.fieldInputError}` : styles.fieldInput}
              type="url"
              placeholder="https://argocd.example.com"
              value={base}
              disabled={submitting}
              onChange={(e) => { setBase(e.target.value); setSaved(false); setUrlError(false); setSaveError(null) }}
              onKeyDown={(e) => { if (e.key === 'Enter') handleSave() }}
            />
            <Button variant="primary" onClick={handleSave} disabled={submitting}>
              Save
            </Button>
            {saved && <span className={styles.savedMsg}>Saved ✓</span>}
            {urlError && <span className={styles.fieldError}>Must be a valid http:// or https:// URL</span>}
            {saveError && <span className={styles.fieldError}>{saveError}</span>}
          </div>
          <p className={styles.fieldHint}>
            Used to generate deep links on the Servings page. Example:{' '}
            <code style={{ fontFamily: 'monospace' }}>https://argocd.example.com</code>
          </p>
        </div>
      </div>
    </div>
  )
}

// ── Image mirrors (General tab) ──────────────────────────────────────────────

function ImageMirrorsSection({ images, onSaved }: { images?: ImagesSettings; onSaved: (s: Settings) => void }) {
  const [mirrors, setMirrors] = useState<ImageMirror[]>(images?.mirrors ?? [])
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => () => {
    if (savedTimer.current) clearTimeout(savedTimer.current)
  }, [])

  function update(index: number, change: Partial<ImageMirror>) {
    setMirrors((prev) => prev.map((m, i) => (i === index ? { ...m, ...change } : m)))
    setSaved(false)
  }

  function validate(): string | null {
    const prefixes = new Set<string>()
    for (const m of mirrors) {
      if (!m.prefix.trim()) return 'Every mirror needs a prefix'
      if (prefixes.has(m.prefix.trim())) return `Prefix "${m.prefix}" is used twice`
      prefixes.add(m.prefix.trim())
      if (!/^(localhost|[^/]*[.:][^/]*)(\/|$)/.test(m.prefix.trim())) {
        return `Prefix "${m.prefix}" must start with a registry host, such as docker.io`
      }
      if (!/^oci:\/\/.+/.test(m.location.trim())) return `The location of "${m.prefix}" must be an oci:// URL`
    }
    return null
  }

  function handleSave() {
    const problem = validate()
    if (problem) {
      setError(problem)
      return
    }
    setError(null)
    setSubmitting(true)
    saveImagesSettings({ mirrors: mirrors.map((m) => ({ prefix: m.prefix.trim(), location: m.location.trim() })) })
      .then((s) => {
        setSaved(true)
        savedTimer.current = setTimeout(() => setSaved(false), 2000)
        onSaved(s)
      })
      .catch((e) => setError((e as Error).message))
      .finally(() => setSubmitting(false))
  }

  return (
    <div className={styles.section}>
      <div className={styles.sectionHeader}>
        <span className={styles.sectionTitle}>Image Mirrors</span>
      </div>
      <div className={styles.sectionBody}>
        <div className={styles.settingsSection}>
          <p className={styles.fieldHint}>
            Kokumi resolves and vendors the images of every render through the mirror
            with the longest matching prefix, such as a pull-through cache or an
            air-gapped copy, and never falls back to the original registry. Manifests
            keep the original names; nodes pull through their own mirror configuration.
          </p>
          {mirrors.length === 0 && <p className={styles.fieldHint}>No mirrors: images are read from their own registries.</p>}
          {mirrors.map((m, i) => (
            <div key={i} className={styles.fieldRow}>
              <input
                aria-label="Prefix"
                className={styles.fieldInput}
                type="text"
                placeholder="docker.io"
                value={m.prefix}
                disabled={submitting}
                onChange={(e) => update(i, { prefix: e.target.value })}
              />
              <input
                aria-label="Location"
                className={styles.fieldInput}
                type="text"
                placeholder="oci://harbor.example.com/dockerhub"
                value={m.location}
                disabled={submitting}
                onChange={(e) => update(i, { location: e.target.value })}
              />
              <Button
                variant="secondary"
                onClick={() => { setMirrors((prev) => prev.filter((_, j) => j !== i)); setSaved(false) }}
                disabled={submitting}
              >
                Remove
              </Button>
            </div>
          ))}
          <div className={styles.fieldRow}>
            <Button
              variant="secondary"
              onClick={() => { setMirrors((prev) => [...prev, { prefix: '', location: '' }]); setSaved(false) }}
              disabled={submitting || mirrors.length >= maxImageMirrors}
            >
              Add mirror
            </Button>
            <Button variant="primary" onClick={handleSave} disabled={submitting}>
              Save
            </Button>
            {saved && <span className={styles.savedMsg}>Saved ✓</span>}
          </div>
          {error && <p className={styles.fieldError}>{error}</p>}
        </div>
      </div>
    </div>
  )
}

// ── Authentication tab ───────────────────────────────────────────────────────

function AuthenticationTab({ auth, onSaved }: { auth?: AuthSettings; onSaved: (s: Settings) => void }) {
  const adminDefaults = { username: 'admin', secretRef: 'kokumi-server-auth' }
  const oidcDefaults = {
    issuer: '',
    clientID: '',
    secretRef: 'kokumi-server-oidc',
    usernameClaim: 'email',
    emailClaim: 'email',
    groupsClaim: 'groups',
    scopes: 'openid, profile, email',
  }
  const [adminEnabled, setAdminEnabled] = useState(auth?.adminUser?.enabled ?? true)
  const [adminUsername, setAdminUsername] = useState(auth?.adminUser?.username ?? adminDefaults.username)
  const [adminSecretRef, setAdminSecretRef] = useState(auth?.adminUser?.secretRef?.name ?? adminDefaults.secretRef)
  // Disabled groups collapse; their field values are retained locally until a
  // successful save. On save with a group disabled, only the enabled flag is
  // persisted (the CRD's kubebuilder defaults refill the omitted fields
  // server-side), and the local fields reset to defaults — mirroring the CR.
  const [oidcEnabled, setOidcEnabled] = useState(auth?.oidc != null)
  const [oidcIssuer, setOidcIssuer] = useState(auth?.oidc?.issuerURL ?? oidcDefaults.issuer)
  const [oidcClientID, setOidcClientID] = useState(auth?.oidc?.clientID ?? oidcDefaults.clientID)
  const [oidcSecretRef, setOidcSecretRef] = useState(auth?.oidc?.clientSecretRef?.name ?? oidcDefaults.secretRef)
  const [usernameClaim, setUsernameClaim] = useState(auth?.oidc?.usernameClaim ?? oidcDefaults.usernameClaim)
  const [emailClaim, setEmailClaim] = useState(auth?.oidc?.emailClaim ?? oidcDefaults.emailClaim)
  const [groupsClaim, setGroupsClaim] = useState(auth?.oidc?.groupsClaim ?? oidcDefaults.groupsClaim)
  const [scopes, setScopes] = useState(auth?.oidc?.scopes?.join(', ') ?? oidcDefaults.scopes)
  const [tokenSecretRef, setTokenSecretRef] = useState(auth?.tokenSigningKeySecretRef?.name ?? 'kokumi-server-tokens')

  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Clear the saved-feedback timer on unmount (tab switch) to avoid a
  // state update on a dead component.
  useEffect(() => () => {
    if (savedTimer.current) clearTimeout(savedTimer.current)
  }, [])

  function resetAdminFields() {
    setAdminUsername(adminDefaults.username)
    setAdminSecretRef(adminDefaults.secretRef)
  }

  function resetOidcFields() {
    setOidcIssuer(oidcDefaults.issuer)
    setOidcClientID(oidcDefaults.clientID)
    setOidcSecretRef(oidcDefaults.secretRef)
    setUsernameClaim(oidcDefaults.usernameClaim)
    setEmailClaim(oidcDefaults.emailClaim)
    setGroupsClaim(oidcDefaults.groupsClaim)
    setScopes(oidcDefaults.scopes)
  }

  function toggleAdmin(enabled: boolean) {
    // Values are kept (hidden only) so an off→on flip without saving restores
    // them; clearing to defaults happens on save while disabled.
    setAdminEnabled(enabled)
    setSaved(false)
  }

  function toggleOidc(enabled: boolean) {
    setOidcEnabled(enabled)
    setSaved(false)
  }

  function validate(): string | null {
    if (adminEnabled) {
      if (adminUsername.trim() === '') return 'Admin username must not be empty'
      if (adminUsername.length > maxClaimLength) return 'Admin username is limited to 253 characters'
      if (/[\\/\s]/.test(adminUsername)) return 'Admin username must not contain / or whitespace'
      if (adminSecretRef.trim() === '') return 'Admin credentials secret reference must not be empty'
    }
    if (tokenSecretRef.trim() === '') return 'Token signing key secret reference must not be empty'
    if (oidcEnabled) {
      try {
        const u = new URL(oidcIssuer.trim())
        if (u.protocol !== 'http:' && u.protocol !== 'https:') {
          return 'OIDC issuer must be a valid http:// or https:// URL'
        }
      } catch {
        return 'OIDC issuer must be a valid http:// or https:// URL'
      }
      if (oidcClientID.trim() === '') return 'OIDC client ID is required when OIDC is enabled'
      if (oidcSecretRef.trim() === '') return 'OIDC client secret reference must not be empty'
      for (const c of [usernameClaim, emailClaim, groupsClaim]) {
        if (c.length > maxClaimLength) return 'OIDC claim names are limited to 253 characters'
      }
    }
    return null
  }

  function handleSave() {
    const problem = validate()
    if (problem) {
      setError(problem)
      return
    }
    setError(null)
    setSubmitting(true)
    // Disabled admin account: only the flag is persisted. username/secretRef
    // are omitted and the CRD's kubebuilder defaults refill them server-side.
    const next: AuthSettings = {
      adminUser: adminEnabled
        ? {
            enabled: true,
            username: adminUsername.trim(),
            secretRef: { name: adminSecretRef.trim() },
          }
        : { enabled: false },
      tokenSigningKeySecretRef: { name: tokenSecretRef.trim() },
    }
    // Toggled-off OIDC is persisted as absence of the oidc object.
    if (oidcEnabled) {
      next.oidc = {
        issuerURL: oidcIssuer.trim().replace(/\/$/, ''),
        clientID: oidcClientID.trim(),
        clientSecretRef: { name: oidcSecretRef.trim() },
        usernameClaim: usernameClaim.trim(),
        emailClaim: emailClaim.trim(),
        groupsClaim: groupsClaim.trim(),
        scopes: scopes.split(',').map((s) => s.trim()).filter(Boolean),
      }
    }
    saveAuthSettings(next)
      .then((s) => {
        // The CR now holds defaults for disabled groups — mirror that locally
        // so re-enabling shows defaults, not the discarded values.
        if (!adminEnabled) resetAdminFields()
        if (!oidcEnabled) resetOidcFields()
        setSaved(true)
        savedTimer.current = setTimeout(() => setSaved(false), 2000)
        onSaved(s)
      })
      .catch((e) => setError((e as Error).message))
      .finally(() => setSubmitting(false))
  }

  return (
    <div className={styles.groupStack}>
      <p className={styles.fieldHint}>
        Changes take effect after the server reloads its configuration and
        may affect who can sign in. Secrets themselves (password hash,
        client secret, signing key) are managed in the cluster; only the
        Secret references are edited here.
      </p>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>Admin Account</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.settingsSection}>
            <div className={styles.fieldRow}>
              <label className={styles.fieldLabel} htmlFor="admin-enabled">Enabled</label>
              <Toggle
                id="admin-enabled"
                checked={adminEnabled}
                disabled={submitting}
                label="Built-in username/password login"
                onChange={toggleAdmin}
              />
            </div>

            {adminEnabled && (
              <>
                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor="admin-username">Username</label>
                  <input
                    id="admin-username"
                    className={styles.fieldInput}
                    type="text"
                    value={adminUsername}
                    disabled={submitting}
                    onChange={(e) => { setAdminUsername(e.target.value); setSaved(false) }}
                  />
                </div>

                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor="admin-secret-ref">Credentials Secret</label>
                  <input
                    id="admin-secret-ref"
                    className={styles.fieldInput}
                    type="text"
                    value={adminSecretRef}
                    disabled={submitting}
                    onChange={(e) => { setAdminSecretRef(e.target.value); setSaved(false) }}
                  />
                </div>
              </>
            )}
          </div>
        </div>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>OIDC</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.settingsSection}>
            <div className={styles.fieldRow}>
              <label className={styles.fieldLabel} htmlFor="oidc-enabled">Enabled</label>
              <Toggle
                id="oidc-enabled"
                checked={oidcEnabled}
                disabled={submitting}
                label="Single sign-on via an external OpenID Connect provider"
                onChange={toggleOidc}
              />
            </div>

            {oidcEnabled && (
              <>
                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor="oidc-issuer">Issuer URL</label>
                  <input
                    id="oidc-issuer"
                    className={styles.fieldInput}
                    type="url"
                    placeholder="https://dex.example.com"
                    value={oidcIssuer}
                    disabled={submitting}
                    onChange={(e) => { setOidcIssuer(e.target.value); setSaved(false) }}
                  />
                </div>

                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor="oidc-client-id">Client ID</label>
                  <input
                    id="oidc-client-id"
                    className={styles.fieldInput}
                    type="text"
                    value={oidcClientID}
                    disabled={submitting}
                    onChange={(e) => { setOidcClientID(e.target.value); setSaved(false) }}
                  />
                </div>

                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor="oidc-secret-ref">Client Secret</label>
                  <input
                    id="oidc-secret-ref"
                    className={styles.fieldInput}
                    type="text"
                    value={oidcSecretRef}
                    disabled={submitting}
                    onChange={(e) => { setOidcSecretRef(e.target.value); setSaved(false) }}
                  />
                </div>

                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor="username-claim">Username claim</label>
                  <input
                    id="username-claim"
                    className={styles.fieldInput}
                    type="text"
                    value={usernameClaim}
                    disabled={submitting}
                    onChange={(e) => { setUsernameClaim(e.target.value); setSaved(false) }}
                  />
                </div>

                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor="email-claim">Email claim</label>
                  <input
                    id="email-claim"
                    className={styles.fieldInput}
                    type="text"
                    value={emailClaim}
                    disabled={submitting}
                    onChange={(e) => { setEmailClaim(e.target.value); setSaved(false) }}
                  />
                </div>

                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor="groups-claim">Groups claim</label>
                  <input
                    id="groups-claim"
                    className={styles.fieldInput}
                    type="text"
                    value={groupsClaim}
                    disabled={submitting}
                    onChange={(e) => { setGroupsClaim(e.target.value); setSaved(false) }}
                  />
                </div>

                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor="oidc-scopes">Scopes</label>
                  <input
                    id="oidc-scopes"
                    className={styles.fieldInput}
                    type="text"
                    value={scopes}
                    disabled={submitting}
                    onChange={(e) => { setScopes(e.target.value); setSaved(false) }}
                  />
                  <span className={styles.fieldHint}>Comma-separated</span>
                </div>
              </>
            )}
          </div>
        </div>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>Token Signing Key</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.settingsSection}>
            <div className={styles.fieldRow}>
              <label className={styles.fieldLabel} htmlFor="token-secret-ref">Secret</label>
              <input
                id="token-secret-ref"
                className={styles.fieldInput}
                type="text"
                value={tokenSecretRef}
                disabled={submitting}
                onChange={(e) => { setTokenSecretRef(e.target.value); setSaved(false) }}
              />
            </div>
            <p className={styles.fieldHint}>
              Holds the HMAC key that signs all session tokens (key
              "signing-key"). Created with a generated key when missing.
            </p>
          </div>
        </div>
      </div>

      {error && <p className={styles.fieldError}>{error}</p>}

      <div className={styles.fieldRow}>
        <Button variant="primary" onClick={handleSave} disabled={submitting}>
          Save
        </Button>
        {saved && <span className={styles.savedMsg}>Saved ✓</span>}
      </div>
    </div>
  )
}

// ── Signing tab ──────────────────────────────────────────────────────────────

/** Matches the name pattern of a trusted key. */
const trustedKeyNamePattern = /^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$/
const maxTrustedKeys = 16

/**
 * SigningTab shows the public key Kokumi signs with and the commands that
 * verify its artifacts, and edits which further keys Kokumi trusts and
 * whether every Product source must verify.
 */
function SigningTab({ signing, onSaved }: { signing?: SigningSettings; onSaved: (s: Settings) => void }) {
  const [info, setInfo] = useState<SigningInfo | null>(null)
  const [infoError, setInfoError] = useState<string | null>(null)
  const [trustedKeys, setTrustedKeys] = useState<TrustedKey[]>(signing?.trustedKeys ?? [])
  const [requireVerification, setRequireVerification] = useState(signing?.requireSourceVerification ?? false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    let cancelled = false
    getSigning()
      .then((s) => { if (!cancelled) setInfo(s) })
      .catch((e) => { if (!cancelled) setInfoError((e as Error).message) })
    return () => {
      cancelled = true
      if (savedTimer.current) clearTimeout(savedTimer.current)
    }
  }, [])

  function updateKey(index: number, change: Partial<TrustedKey>) {
    setTrustedKeys((prev) => prev.map((k, i) => (i === index ? { ...k, ...change } : k)))
    setSaved(false)
  }

  function removeKey(index: number) {
    setTrustedKeys((prev) => prev.filter((_, i) => i !== index))
    setSaved(false)
  }

  function addKey(key: TrustedKey) {
    setTrustedKeys((prev) => [...prev, key])
    setSaved(false)
  }

  function validate(): string | null {
    if (trustedKeys.length > maxTrustedKeys) return `At most ${maxTrustedKeys} trusted keys`
    const names = new Set<string>()
    for (const k of trustedKeys) {
      if (!trustedKeyNamePattern.test(k.name) || k.name.length > 63) {
        return `Key name "${k.name}" must be lowercase letters, digits, "-" and ".", at most 63 characters`
      }
      if (names.has(k.name)) return `Key name "${k.name}" is used twice`
      names.add(k.name)
      if (!k.publicKey.trim().startsWith('-----BEGIN PUBLIC KEY-----')) {
        return `Key "${k.name}" must be a PEM-encoded public key`
      }
    }
    return null
  }

  function handleSave() {
    const problem = validate()
    if (problem) {
      setError(problem)
      return
    }
    setError(null)
    setSubmitting(true)
    saveSigningSettings({
      trustedKeys: trustedKeys.map((k) => ({ name: k.name.trim(), publicKey: k.publicKey.trim() + '\n' })),
      requireSourceVerification: requireVerification,
    })
      .then((s) => {
        setSaved(true)
        savedTimer.current = setTimeout(() => setSaved(false), 2000)
        onSaved(s)
      })
      .catch((e) => setError((e as Error).message))
      .finally(() => setSubmitting(false))
  }

  function downloadPublicKey(publicKey: string) {
    const url = URL.createObjectURL(new Blob([publicKey], { type: 'application/x-pem-file' }))
    const link = document.createElement('a')
    link.href = url
    link.download = 'kokumi.pub'
    link.click()
    URL.revokeObjectURL(url)
  }

  const current = info?.current
  const currentTrusted = current != null && trustedKeys.some((k) => k.publicKey.trim() === current.publicKey.trim())

  return (
    <div className={styles.groupStack}>
      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>Signing Key</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.settingsSection}>
            {infoError && <p className={styles.fieldError}>Failed to load the signing key: {infoError}</p>}
            {info && !info.ready && (
              <p className={styles.fieldError}>The signing key is not ready{info.message ? `: ${info.message}` : ''}</p>
            )}
            {current ? (
              <>
                <div className={styles.fieldRow}>
                  <span className={styles.fieldLabel}>Key ID</span>
                  <code>{current.keyID}</code>
                </div>
                <pre className={styles.codeBlock}>{current.publicKey}</pre>
                <div className={styles.fieldRow}>
                  <Button variant="secondary" onClick={() => downloadPublicKey(current.publicKey)}>
                    Download kokumi.pub
                  </Button>
                </div>
                <p className={styles.fieldHint}>
                  Every artifact and attestation Kokumi writes is signed with
                  this key. Anyone can verify an artifact offline with the
                  stock cosign CLI:
                </p>
                <pre className={styles.codeBlock}>{[
                  `kubectl -n kokumi get kitchen default -o jsonpath='{.status.signing.publicKey}' > kokumi.pub`,
                  'cosign verify --key kokumi.pub --insecure-ignore-tlog=true <registry>/<repository>@<digest>',
                  'cosign verify-attestation --key kokumi.pub --insecure-ignore-tlog=true --type slsaprovenance1 <registry>/<repository>@<digest>',
                ].join('\n')}</pre>
              </>
            ) : (
              !infoError && <p className={styles.fieldHint}>Loading…</p>
            )}
            <p className={styles.fieldHint}>
              The private key lives in the Secret <code>kokumi-signing-key</code> in
              the namespace <code>kokumi-signing</code>,
              which only the controller and cluster admins can read or write.
              To rotate it, trust the current key below, save, and delete the
              Secret as a cluster admin; the controller generates a new key.
            </p>
          </div>
        </div>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>Trusted Keys</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.settingsSection}>
            <p className={styles.fieldHint}>
              Further public keys whose signatures Kokumi trusts for its own
              artifacts: the key used before a rotation, or the key of another
              installation whose releases are imported.
            </p>
            {trustedKeys.length === 0 && <p className={styles.fieldHint}>No further keys are trusted.</p>}
            {trustedKeys.map((k, i) => (
              <div key={i} className={styles.settingsSection}>
                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor={`trusted-key-name-${i}`}>Name</label>
                  <input
                    id={`trusted-key-name-${i}`}
                    className={styles.fieldInput}
                    type="text"
                    placeholder="before-2026-10"
                    value={k.name}
                    disabled={submitting}
                    onChange={(e) => updateKey(i, { name: e.target.value })}
                  />
                  <Button variant="secondary" onClick={() => removeKey(i)} disabled={submitting}>
                    Remove
                  </Button>
                </div>
                <div className={styles.fieldRow}>
                  <label className={styles.fieldLabel} htmlFor={`trusted-key-pem-${i}`}>Public key</label>
                  <textarea
                    id={`trusted-key-pem-${i}`}
                    className={styles.fieldTextarea}
                    rows={5}
                    placeholder={'-----BEGIN PUBLIC KEY-----\n…\n-----END PUBLIC KEY-----'}
                    value={k.publicKey}
                    disabled={submitting}
                    onChange={(e) => updateKey(i, { publicKey: e.target.value })}
                  />
                </div>
              </div>
            ))}
            <div className={styles.fieldRow}>
              <Button
                variant="secondary"
                onClick={() => addKey({ name: '', publicKey: '' })}
                disabled={submitting || trustedKeys.length >= maxTrustedKeys}
              >
                Add key
              </Button>
              {current && !currentTrusted && (
                <Button
                  variant="secondary"
                  onClick={() => addKey({ name: `before-${new Date().toISOString().slice(0, 10)}`, publicKey: current.publicKey })}
                  disabled={submitting || trustedKeys.length >= maxTrustedKeys}
                >
                  Trust the current key
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>Source Verification</span>
        </div>
        <div className={styles.sectionBody}>
          <div className={styles.settingsSection}>
            <div className={styles.fieldRow}>
              <label className={styles.fieldLabel} htmlFor="require-source-verification">Required</label>
              <Toggle
                id="require-source-verification"
                checked={requireVerification}
                disabled={submitting}
                label="Every Product source must verify"
                onChange={(v) => { setRequireVerification(v); setSaved(false) }}
              />
            </div>
            <p className={styles.fieldHint}>
              A source verifies through the Product&apos;s own verification
              settings, or by a signature from a key trusted here. A Product
              whose source does not verify is not ready, and its Orders render
              nothing.
            </p>
          </div>
        </div>
      </div>

      {error && <p className={styles.fieldError}>{error}</p>}

      <div className={styles.fieldRow}>
        <Button variant="primary" onClick={handleSave} disabled={submitting}>
          Save
        </Button>
        {saved && <span className={styles.savedMsg}>Saved ✓</span>}
      </div>
    </div>
  )
}
