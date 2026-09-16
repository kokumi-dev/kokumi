import { useState } from 'react'
import type { Menu, MenuFormData, Order, OrderFormData } from '../api/types'
import { updateMenu, deleteMenu, createOrder } from '../api/client'
import { useMenus } from '../hooks/useMenus'
import { useOrders } from '../hooks/useOrders'
import MenuRelationGraph from '../components/menu/MenuRelationGraph'
import MenuFormModal from '../components/menu/MenuFormModal'
import OrderFormModal from '../components/order/OrderFormModal'
import Badge from '../components/shared/Badge'
import Btn from '../components/shared/Btn'
import styles from './MenuDetailPage.module.css'

type FormModalState = null | { mode: 'edit'; menu: Menu }
type OrderModalState = null | { menu: Menu }

interface Props {
  namespace: string
  name: string
  onBack: () => void
  onOpenOrder: (order: Order) => void
}

export default function MenuDetailPage({ namespace, name, onBack, onOpenOrder }: Props) {
  const menus = useMenus()
  const orders = useOrders()
  const [formModal, setFormModal] = useState<FormModalState>(null)
  const [orderModal, setOrderModal] = useState<OrderModalState>(null)

  // Derive the live Menu from the SSE-backed list so it stays fresh.
  const menu: Menu | undefined = menus?.find(
    (m) => m.namespace === namespace && m.name === name,
  )

  if (!menu) {
    return (
      <div className={styles.page}>
        <button className={styles.backBtn} onClick={onBack}>← Back to Menus</button>
        <div className={styles.placeholder}>
          {menus === null ? 'Loading…' : `Menu ${namespace}/${name} not found.`}
        </div>
      </div>
    )
  }

  async function handleUpdate(data: MenuFormData) {
    if (formModal?.mode !== 'edit') return
    const { menu } = formModal
    await updateMenu(menu.namespace, menu.name, data)
    setFormModal(null)
  }

  function openEdit(m: Menu) {
    setFormModal({ mode: 'edit', menu: m })
  }

  function openOrder(m: Menu) {
    setOrderModal({ menu: m })
  }

  async function handleDelete(m: Menu) {
    if (!window.confirm(`Delete Menu "${m.namespace}/${m.name}"?`)) return
    await deleteMenu(m.namespace, m.name)
    onBack()
  }

  async function handleOrder(data: OrderFormData) {
    await createOrder(data)
    setOrderModal(null)
  }

  return (
    <div className={styles.page}>
      <button className={styles.backBtn} onClick={onBack}>← Back to Menus</button>

      <div className={styles.header}>
        <div className={styles.headerLeft}>
          <span className={styles.title}>{menu.name}</span>
          <span className={styles.subtitle}>namespace: {menu.namespace}</span>
        </div>
        <div className={styles.headerActions}>
          <Badge state={menu.state ?? ''} />
          <Btn variant="primary" size="sm" onClick={() => openOrder(menu)}>Order</Btn>
          <Btn variant="secondary" size="sm" onClick={() => openEdit(menu)}>Edit</Btn>
          <Btn variant="danger" size="sm" onClick={() => handleDelete(menu)}>Delete</Btn>
        </div>
      </div>

      <div className={styles.section}>
        <span className={styles.sectionTitle}>Orders</span>
        <MenuRelationGraph menu={menu} orders={orders} onSelectOrder={onOpenOrder} />
      </div>

      <div className={styles.card}>
        <span className={styles.sectionTitle}>Spec</span>
        <div className={styles.specGrid}>
          <span className={styles.specKey}>Source OCI</span>
          <span className={styles.specValue}>
            {menu.source.pantryRef?.name ? `pantry: ${menu.source.pantryRef.name}` : menu.source.oci}
          </span>
          <span className={styles.specKey}>Version</span>
          <span className={styles.specValue}>{menu.source.version}</span>
          <span className={styles.specKey}>Auto Deploy Default</span>
          <span className={styles.specValue}>{menu.defaults.autoDeploy}</span>
          {menu.render?.helm && (
            <>
              <span className={styles.specKey}>Renderer</span>
              <span className={styles.specValue}>Helm</span>
            </>
          )}
        </div>
      </div>

      <div className={styles.card}>
        <span className={styles.sectionTitle}>Override Policies</span>
        <div className={styles.specGrid}>
          <span className={styles.specKey}>Values Policy</span>
          <span className={styles.specValue}>{menu.overrides.values.policy}</span>
          {menu.overrides.values.policy === 'Restricted' && menu.overrides.values.allowed && (
            <>
              <span className={styles.specKey}>Allowed Values</span>
              <span className={styles.specValue}>{menu.overrides.values.allowed.join(', ')}</span>
            </>
          )}
          <span className={styles.specKey}>Patches Policy</span>
          <span className={styles.specValue}>{menu.overrides.patches.policy}</span>
          {menu.overrides.patches.policy === 'Restricted' && menu.overrides.patches.allowed && (
            <>
              <span className={styles.specKey}>Allowed Patches</span>
              <span className={styles.specValue}>
                {menu.overrides.patches.allowed.map((a) =>
                  `${a.target.kind}/${a.target.name}: ${a.paths.join(', ')}`,
                ).join('; ')}
              </span>
            </>
          )}
        </div>
      </div>

      {menu.patches && menu.patches.length > 0 && (
        <div className={styles.card}>
          <span className={styles.sectionTitle}>Base Patches</span>
          {menu.patches.map((p, i) => (
            <div key={i} className={styles.specGrid}>
              <span className={styles.specKey}>Target</span>
              <span className={styles.specValue}>
                {p.target.kind}/{p.target.name}
                {p.target.namespace ? ` (${p.target.namespace})` : ''}
              </span>
              {Object.entries(p.set).map(([k, v]) => (
                <span key={k} className={styles.specValue} style={{ gridColumn: '1 / -1' }}>
                  {k}: {v}
                </span>
              ))}
            </div>
          ))}
        </div>
      )}

      {menu.conditions && menu.conditions.length > 0 && (
        <div className={styles.card}>
          <span className={styles.sectionTitle}>Conditions</span>
          {menu.conditions.map((c) => (
            <div key={c.type} className={styles.specGrid}>
              <span className={styles.specKey}>{c.type}</span>
              <span className={styles.specValue}>
                {c.status} — {c.message}
              </span>
            </div>
          ))}
        </div>
      )}

      {formModal && <MenuFormModal menu={menu} onSubmit={handleUpdate} onClose={() => setFormModal(null)} />}
      {orderModal && (
        <OrderFormModal
          menuRef={{ name: orderModal.menu.name }}
          menu={orderModal.menu}
          onSubmit={handleOrder}
          onClose={() => setOrderModal(null)}
        />
      )}
    </div>
  )
}
