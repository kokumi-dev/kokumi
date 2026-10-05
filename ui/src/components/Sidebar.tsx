import { NavLink } from 'react-router'
import styles from './Sidebar.module.css'
import logo from '../assets/logo.png'
import { getUsername } from '../api/auth'

interface NavItem {
  to: string
  label: string
  icon: React.ReactNode
  /** Only shown when the signed-in user is an admin. */
  adminOnly?: boolean
}

interface NavSection {
  label?: string
  items: NavItem[]
}

interface Props {
  operatorVersion?: string
  onLogout?: () => void
  /** When false, the admin-only Settings entry is hidden. */
  isAdmin: boolean
}

function IconDashboard() {
  return (
    <svg className={styles.navIcon} viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="1" y="1" width="6.5" height="6.5" rx="1.2" />
      <rect x="10.5" y="1" width="6.5" height="6.5" rx="1.2" />
      <rect x="1" y="10.5" width="6.5" height="6.5" rx="1.2" />
      <rect x="10.5" y="10.5" width="6.5" height="6.5" rx="1.2" />
    </svg>
  )
}

function IconOrder() {
  return (
    <svg className={styles.navIcon} viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M4 1v16M4 6h6a3 3 0 0 1 0 6H4" />
    </svg>
  )
}

function IconPreparation() {
  return (
    <svg className={styles.navIcon} viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6 1v4a3 3 0 0 0 6 0V1" />
      <path d="M3 8h12l-1.5 8H4.5L3 8Z" />
    </svg>
  )
}

function IconServing() {
  return (
    <svg className={styles.navIcon} viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="9" cy="9.5" r="6" />
      <path d="M3 9.5h12" />
      <path d="M9 1v2.5" />
    </svg>
  )
}

function IconSettings() {
  return (
    <svg className={styles.navIcon} viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="9" cy="9" r="2.5" />
      <path d="M9 1v2M9 15v2M1 9h2M15 9h2M3.1 3.1l1.4 1.4M13.5 13.5l1.4 1.4M3.1 14.9l1.4-1.4M13.5 4.5l1.4-1.4" />
    </svg>
  )
}

function IconPantry() {
  return (
    <svg className={styles.navIcon} viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2" y="5" width="14" height="10" rx="2" />
      <path d="M5 5V3.5A1.5 1.5 0 0 1 6.5 2h5A1.5 1.5 0 0 1 13 3.5V5" />
      <path d="M6 9h6M6 12h4" />
    </svg>
  )
}

function IconMenu() {
  return (
    <svg className={styles.navIcon} viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2" y="2" width="14" height="14" rx="2" />
      <path d="M6 6h6M6 9h6M6 12h4" />
    </svg>
  )
}

const sections: NavSection[] = [
  {
    label: 'Overview',
    items: [
      { to: '/', label: 'Dashboard', icon: <IconDashboard /> },
    ],
  },
  {
    label: 'Resources',
    items: [
      { to: '/orders',       label: 'Orders',       icon: <IconOrder /> },
      { to: '/menus',        label: 'Menus',        icon: <IconMenu /> },
      { to: '/pantries',     label: 'Pantries',     icon: <IconPantry /> },
      { to: '/preparations', label: 'Preparations', icon: <IconPreparation /> },
      { to: '/servings',     label: 'Servings',     icon: <IconServing /> },
    ],
  },
  {
    label: 'System',
    items: [
      { to: '/settings', label: 'Settings', icon: <IconSettings />, adminOnly: true },
    ],
  },
]

export default function Sidebar({ operatorVersion, onLogout, isAdmin }: Props) {
  const visibleSections = sections
    .map((section) => ({
      ...section,
      items: section.items.filter((item) => !item.adminOnly || isAdmin),
    }))
    .filter((section) => section.items.length > 0)

  return (
    <aside className={styles.sidebar}>
      {/* ── Logo ── */}
      <div className={styles.logo}>
        <img src={logo} alt="Kokumi" className={styles.logoMark} />
        <div className={styles.logoText}>Kokumi</div>
        <div className={styles.logoSub}>Operator Console</div>
      </div>

      {/* ── Navigation ── */}
      <nav className={styles.nav}>
        {visibleSections.map((section) => (
          <div key={section.label ?? 'default'} className={styles.navSection}>
            {section.label && (
              <div className={styles.navSectionLabel}>{section.label}</div>
            )}
            {section.items.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.to === '/'}
                className={({ isActive }) => `${styles.navItem} ${isActive ? styles.active : ''}`}
              >
                {item.icon}
                {item.label}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>

      {/* ── Footer ── */}
      <div className={styles.footer}>
        {onLogout && (
          <button className={styles.logout} onClick={onLogout}>
            Sign out
          </button>
        )}
        {getUsername() && (
          <div className={styles.userInfo}>
            <span className={styles.userName}>{getUsername()}</span>
          </div>
        )}
        {operatorVersion && (
          <div className={styles.footerInfo}>
            Version{' '}
            <span className={styles.footerInfoValue}>{operatorVersion}</span>
          </div>
        )}
        <div className={styles.footerInfo}>
          API Group{' '}
          <span className={styles.footerInfoValue}>delivery.kokumi.dev</span>
        </div>
      </div>
    </aside>
  )
}
