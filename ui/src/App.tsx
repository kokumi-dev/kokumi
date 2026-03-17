import { useEffect, useState } from 'react'
import styles from './App.module.css'
import Sidebar, { type Page } from './components/Sidebar'
import Dashboard from './pages/Dashboard'
import Orders from './pages/Orders'
import Preparations from './pages/Preparations'
import Servings from './pages/Servings'
import Settings from './pages/Settings'

interface Info {
  name: string
  version: string
}

function App() {
  const [activePage, setActivePage] = useState<Page>('dashboard')
  const [info, setInfo] = useState<Info | null>(null)

  useEffect(() => {
    fetch('/api/v1/info')
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        return res.json() as Promise<Info>
      })
      .then(setInfo)
      .catch(() => {/* silently ignore in dev */})
  }, [])

  function renderPage() {
    switch (activePage) {
      case 'dashboard':
        return <Dashboard operatorName={info?.name} operatorVersion={info?.version} />
      case 'orders':
        return <Orders />
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
      />
      <main className={styles.content}>
        {renderPage()}
      </main>
    </div>
  )
}

export default App

