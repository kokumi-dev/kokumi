import { useEffect, useState } from 'react'
import styles from './App.module.css'
import Sidebar, { type Page } from './components/Sidebar'
import Dashboard from './pages/Dashboard'
import Orders from './pages/Orders'
import Menus from './pages/Menus'
import Pantries from './pages/Pantries'
import Preparations from './pages/Preparations'
import Servings from './pages/Servings'
import Settings from './pages/Settings'
import Login from './pages/Login'
import { logout, onAuthChange, refresh, getTokenTTL, isAuthed } from './api/auth'

interface Info {
  name: string
  version: string
  authEnabled: boolean
}

function App() {
  const [activePage, setActivePage] = useState<Page>('dashboard')
  const [info, setInfo] = useState<Info | null>(null)
  const [ready, setReady] = useState(false)
  const [authed, setAuthed] = useState(() => isAuthed())
  const [bootRefreshDone, setBootRefreshDone] = useState(false)

  useEffect(() => {
    fetch('/api/v1/info')
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        return res.json() as Promise<Info>
      })
      .then((data) => {
        setInfo(data)
      })
      .catch(() => {/* silently ignore in dev */})
      .finally(() => setReady(true))
  }, [])

  useEffect(() => {
    if (!ready) return
    const authRequired = info?.authEnabled ?? false
    if (!authRequired || isAuthed()) {
      return
    }
    void refresh().then((ok) => {
      if (ok) setAuthed(true)
      setBootRefreshDone(true)
    })
  }, [ready, info])

  // Keep the authed flag in sync with token changes (login, logout, 401).
  useEffect(() => onAuthChange(() => setAuthed(isAuthed())), [])

  // Proactively refresh the access token shortly before it expires, so the SSE
  // dashboard stream and API calls never hit a 401.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null
    const schedule = () => {
      const ttl = getTokenTTL()
      if (ttl <= 0) return
      // Refresh at 80% of the remaining lifetime, leaving a comfortable margin
      // for latency. getTokenTTL derives this from the token's exp claim.
      const delayMs = Math.max(1000, Math.floor(ttl * 800))
      timer = setTimeout(() => {
        void refresh().then((ok) => {
          if (ok) schedule()
        })
      }, delayMs)
    }
    schedule()
    const unsub = onAuthChange(() => {
      if (timer) clearTimeout(timer)
      schedule()
    })
    return () => {
      if (timer) clearTimeout(timer)
      unsub()
    }
  }, [])

  // Wait until /api/v1/info resolves. If auth is required and we have no valid
  // token, hold off showing the login screen until the boot-time silent
  // refresh attempt has completed (it may authenticate via the shared cookie).
  if (!ready) return null

  const authRequired = info?.authEnabled ?? false
  if (authRequired && !authed && !bootRefreshDone) return null
  if (authRequired && !authed) {
    return (
      <Login
        operatorVersion={info?.version}
        onSuccess={() => setAuthed(true)}
      />
    )
  }

  function renderPage() {
    switch (activePage) {
      case 'dashboard':
        return <Dashboard operatorName={info?.name} operatorVersion={info?.version} />
      case 'orders':
        return <Orders />
      case 'menus':
        return <Menus />
      case 'pantries':
        return <Pantries />
      case 'preparations':
        return <Preparations />
      case 'servings':
        return <Servings />
      case 'settings':
        return <Settings />
    }
  }

  return (
    <div className={styles.layout}>
      <Sidebar
        activePage={activePage}
        onNavigate={setActivePage}
        operatorVersion={info?.version}
        onLogout={authRequired ? () => void logout() : undefined}
      />
      <main className={styles.content}>
        {renderPage()}
      </main>
    </div>
  )
}

export default App

