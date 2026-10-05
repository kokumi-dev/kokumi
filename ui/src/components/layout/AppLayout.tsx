import { Outlet, ScrollRestoration } from 'react-router'
import Sidebar from '../Sidebar'
import { useAppInfo } from '../../appContext'
import styles from '../../App.module.css'

export default function AppLayout() {
  const { operatorVersion, isAdmin, onLogout } = useAppInfo()

  return (
    <div className={styles.layout}>
      <Sidebar operatorVersion={operatorVersion} onLogout={onLogout} isAdmin={isAdmin === true} />
      <main className={styles.content}>
        <Outlet />
      </main>
      <ScrollRestoration />
    </div>
  )
}
