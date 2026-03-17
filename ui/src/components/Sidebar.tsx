import styles from './Sidebar.module.css'
import logo from '../assets/logo.png'

export type Page = 'dashboard' | 'orders' | 'preparations' | 'servings' | 'settings'

interface NavItem {
  id: Page
  label: string
  icon: React.ReactNode
}

interface NavSection {
  label?: string
  items: NavItem[]
}

interface Props {
  activePage: Page
  onNavigate: (page: Page) => void
  operatorVersion?: string
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

const sections: NavSection[] = [
  {
    label: 'Overview',
    items: [
      { id: 'dashboard', label: 'Dashboard', icon: <IconDashboard /> },
    ],
  },
  {
    label: 'Resources',
    items: [
      { id: 'orders',       label: 'Orders',       icon: <IconOrder /> },
      { id: 'preparations', label: 'Preparations', icon: <IconPreparation /> },
      { id: 'servings',     label: 'Servings',     icon: <IconServing /> },
    ],
  },
  {
    label: 'System',
    items: [
      { id: 'settings', label: 'Settings', icon: <IconSettings /> },
    ],
  },
]

export default function Sidebar({ activePage, onNavigate, operatorVersion }: Props) {
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
        {sections.map((section) => (
          <div key={section.label ?? 'default'} className={styles.navSection}>
            {section.label && (
              <div className={styles.navSectionLabel}>{section.label}</div>
            )}
            {section.items.map((item) => (
              <button
                key={item.id}
                className={`${styles.navItem} ${activePage === item.id ? styles.active : ''}`}
                onClick={() => onNavigate(item.id)}
              >
                {item.icon}
                {item.label}
              </button>
            ))}
          </div>
        ))}
      </nav>

      {/* ── Footer ── */}
      <div className={styles.footer}>
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
