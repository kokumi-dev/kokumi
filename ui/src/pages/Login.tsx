import { useEffect, useState, type SubmitEvent } from 'react'
import styles from './Login.module.css'
import logo from '../assets/logo.png'
import { login } from '../api/auth'
import { Button } from '../components/ui'

// Messages for the sso_error codes the server's OIDC flow redirects back with.
const ssoErrorMessages: Record<string, string> = {
  unavailable: 'The identity provider could not be reached. Try again in a moment.',
}

/** Returns the message for the sso_error in the URL, or null when there is none. */
function ssoErrorMessage(): string | null {
  const code = new URLSearchParams(window.location.search).get('sso_error')
  if (code === null) return null
  return ssoErrorMessages[code] ?? 'Single sign-on failed. Try again.'
}

interface Props {
  /** Called after a successful login so the parent can render the app. */
  onSuccess: () => void
  operatorVersion?: string
  /** Enabled identity providers from the server (subset of "admin", "oidc"). */
  authProviders?: string[]
}

export default function Login({ onSuccess, operatorVersion, authProviders }: Props) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [ssoError] = useState(ssoErrorMessage)

  // Strip the sso_error once it is shown, so a reload does not show it again.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    if (!params.has('sso_error')) return
    params.delete('sso_error')
    const query = params.toString()
    history.replaceState(null, '', window.location.pathname + (query ? `?${query}` : '') + window.location.hash)
  }, [])

  // Render only the providers the server advertises. An empty list means no
  // login method is configured; undefined (no /info response yet) falls back
  // to the admin form so the page still renders.
  const providers = authProviders ?? ['admin']
  const showAdmin = providers.includes('admin')
  const showOIDC = providers.includes('oidc')
  const noProvider = providers.length === 0

  async function handleSubmit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await login(username, password)
      onSuccess()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setSubmitting(false)
    }
  }

  function startSSO() {
    // The server derives the redirect URI from the request host, so a relative path works behind any proxy.
    window.location.href = '/api/v1/auth/oidc/start'
  }

  return (
    <div className={styles.screen}>
      <form className={styles.card} onSubmit={handleSubmit}>
        <div className={styles.header}>
          <img src={logo} alt="Kokumi" className={styles.logo} />
          <div className={styles.title}>Kokumi</div>
          <div className={styles.subtitle}>Operator Console</div>
        </div>

        {noProvider && (
          <div className={styles.error}>
            No login method is configured. Contact your administrator to set up
            authentication.
          </div>
        )}

        {showOIDC && (
          <Button variant="secondary" block onClick={startSSO}>
            Sign in with SSO
          </Button>
        )}

        {ssoError && <div className={styles.error}>{ssoError}</div>}

        {showOIDC && showAdmin && (
          <div className={styles.divider}>
            <span>or</span>
          </div>
        )}

        {showAdmin && (
          <>
            <label className={styles.field}>
              <span className={styles.label}>Username</span>
              <input
                className={styles.input}
                type="text"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoFocus
                required
              />
            </label>

            <label className={styles.field}>
              <span className={styles.label}>Password</span>
              <input
                className={styles.input}
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </label>

            {error && <div className={styles.error}>{error}</div>}

            <Button variant="primary" block type="submit" disabled={submitting}>
              {submitting ? 'Signing in…' : 'Sign in'}
            </Button>
          </>
        )}

        {operatorVersion && (
          <div className={styles.footer}>Version {operatorVersion}</div>
        )}
      </form>
    </div>
  )
}
