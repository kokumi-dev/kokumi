import { Outlet, ScrollRestoration } from 'react-router'
import Sidebar from '../Sidebar'
import { useAppInfo } from '../../appContext'
import type { Capability } from '../../api/capabilities'
import styles from '../../App.module.css'

const noCapabilities: ReadonlySet<Capability> = new Set()

export default function AppLayout() {
  const { operatorVersion, capabilities, onLogout } = useAppInfo()

  return (
    <div className={styles.layout}>
      <Sidebar operatorVersion={operatorVersion} onLogout={onLogout} capabilities={capabilities ?? noCapabilities} />
      <main className={styles.content}>
        <Outlet />
      </main>
      <ScrollRestoration />
    </div>
  )
}
