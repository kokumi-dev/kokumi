import { useState, type FormEvent } from 'react'
import styles from './Login.module.css'
import logo from '../assets/logo.png'
import { login } from '../api/auth'

interface Props {
  /** Called after a successful login so the parent can render the app. */
  onSuccess: () => void
  operatorVersion?: string
}

export default function Login({ onSuccess, operatorVersion }: Props) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(e: FormEvent) {
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

  return (
    <div className={styles.screen}>
      <form className={styles.card} onSubmit={handleSubmit}>
        <div className={styles.header}>
          <img src={logo} alt="Kokumi" className={styles.logo} />
          <div className={styles.title}>Kokumi</div>
          <div className={styles.subtitle}>Operator Console</div>
        </div>

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

        <button className={styles.submit} type="submit" disabled={submitting}>
          {submitting ? 'Signing in…' : 'Sign in'}
        </button>

        {operatorVersion && (
          <div className={styles.footer}>Version {operatorVersion}</div>
        )}
      </form>
    </div>
  )
}
