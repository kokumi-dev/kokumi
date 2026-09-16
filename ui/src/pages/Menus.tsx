import { useState } from 'react'
import type { Menu, MenuFormData, OrderFormData } from '../api/types'
import { createMenu, updateMenu, createOrder } from '../api/client'
import { useMenus } from '../hooks/useMenus'
import MenuList from '../components/menu/MenuList'
import MenuFormModal from '../components/menu/MenuFormModal'
import OrderFormModal from '../components/order/OrderFormModal'
import Btn from '../components/shared/Btn'
import styles from './pages.module.css'

type FormModalState = null | { mode: 'add' } | { mode: 'edit'; menu: Menu }
type OrderModalState = null | { menu: Menu }

interface Props {
  onOpenMenuDetail: (key: { namespace: string; name: string }) => void
}

export default function MenusPage({ onOpenMenuDetail }: Props) {
  const menus = useMenus()
  const [formModal, setFormModal] = useState<FormModalState>(null)
  const [orderModal, setOrderModal] = useState<OrderModalState>(null)
  const [query, setQuery] = useState('')

  async function handleCreate(data: MenuFormData) {
    await createMenu(data)
    setFormModal(null)
  }

  async function handleUpdate(data: MenuFormData) {
    if (formModal?.mode !== 'edit') return
    const { menu } = formModal
    await updateMenu(menu.namespace, menu.name, data)
    setFormModal(null)
  }

  function openOrder(m: Menu) {
    setOrderModal({ menu: m })
  }

  async function handleOrder(data: OrderFormData) {
    await createOrder(data)
    setOrderModal(null)
  }

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Menus</h1>
        <p className={styles.subtitle}>Manage reusable component templates for Orders</p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>All Menus</span>
          <input className={styles.sectionSearch} type="search" placeholder="Filter by name…" value={query} onChange={(e) => setQuery(e.target.value)} />
          <Btn variant="primary" size="sm" onClick={() => setFormModal({ mode: 'add' })}>+ Add Menu</Btn>
        </div>
        <div className={styles.sectionBody}>
          {menus === null ? (
            <div className={styles.placeholder}><span className={styles.placeholderText}>Loading…</span></div>
          ) : (
            <MenuList
              menus={menus}
              query={query}
              onSelect={onOpenMenuDetail}
              onOrder={openOrder}
            />
          )}
        </div>
      </div>

      {formModal?.mode === 'add' && <MenuFormModal onSubmit={handleCreate} onClose={() => setFormModal(null)} />}
      {formModal?.mode === 'edit' && <MenuFormModal menu={formModal.menu} onSubmit={handleUpdate} onClose={() => setFormModal(null)} />}
      {orderModal && <OrderFormModal menuRef={{ name: orderModal.menu.name }} menu={orderModal.menu} onSubmit={handleOrder} onClose={() => setOrderModal(null)} />}
    </div>
  )
}
