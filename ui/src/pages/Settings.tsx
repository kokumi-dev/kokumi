import { useEffect, useRef, useState } from 'react'
import { Navigate, useParams } from 'react-router'
import styles from './pages.module.css'
import layout from '../components/layout/layout.module.css'
import Toggle from '../components/shared/Toggle'
import { Button, TabNav } from '../components/ui'
import PageHeader from '../components/layout/PageHeader'
import { paths } from '../routes/paths'
import {
  getSettings,
  saveSettings,
  saveAuthSettings,
} from '../api/client'
import type { AuthSettings, Settings } from '../api/client'

const tabs = ['general', 'authentication'] as const
type Tab = typeof tabs[number]

const maxClaimLength = 253

/**
 * Settings shows the operator configuration of the Kitchen singleton in two
 * tabs: General (Argo CD URL) and Authentication (admin user, OIDC, token
 * signing key). The page is only reachable for admins; the server still
 * enforces RBAC on every write.
 */
export default function Settings() {
  const params = useParams()
  const tab = params.tab as Tab
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

  if (!tabs.includes(tab)) {
    return <Navigate to={paths.settings()} replace />
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

      <TabNav
        tabs={[
          { to: paths.settings('general'), label: 'General' },
          { to: paths.settings('authentication'), label: 'Authentication' },
        ]}
      />

      {loading ? (
        <p className={styles.fieldHint}>Loading…</p>
      ) : tab === 'general' ? (
        <GeneralTab key={`g-${settingsRev}`} argoCDURL={settings?.argoCDURL ?? ''} onSaved={handleSaved} />
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
