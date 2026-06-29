import { useEffect, useState } from 'react'
import styles from './App.module.css'
import Sidebar, { type Page } from './components/Sidebar'
import Dashboard from './pages/Dashboard'
import Orders from './pages/Orders'
import Menus from './pages/Menus'
import Preparations from './pages/Preparations'
import Servings from './pages/Servings'
import Settings from './pages/Settings'
import Login from './pages/Login'
import { getToken, logout, onAuthChange } from './api/auth'

interface Info {
  name: string
  version: string
  authEnabled: boolean
}

function App() {
  const [activePage, setActivePage] = useState<Page>('dashboard')
  const [info, setInfo] = useState<Info | null>(null)
  const [ready, setReady] = useState(false)
  const [authed, setAuthed] = useState(() => getToken() !== null)

  useEffect(() => {
    fetch('/api/v1/info')
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        return res.json() as Promise<Info>
      })
      .then(setInfo)
      .catch(() => {/* silently ignore in dev */})
      .finally(() => setReady(true))
  }, [])

  // Keep the authed flag in sync with token changes (login, logout, 401).
  useEffect(() => onAuthChange(() => setAuthed(getToken() !== null)), [])

  // Wait until /api/v1/info resolves so we know whether auth is required.
  if (!ready) return null

  const authRequired = info?.authEnabled ?? false
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
        onLogout={authRequired ? logout : undefined}
      />
      <main className={styles.content}>
        {renderPage()}
      </main>
    </div>
  )
}

export default App

