import { useState } from 'react'
import type { Menu, MenuFormData, OrderFormData } from '../api/types'
import { createMenu, updateMenu, deleteMenu, createOrder } from '../api/client'
import { useMenus } from '../hooks/useMenus'
import MenuList from '../components/menu/MenuList'
import MenuDetail from '../components/menu/MenuDetail'
import MenuFormModal from '../components/menu/MenuFormModal'
import OrderFormModal from '../components/order/OrderFormModal'
import Btn from '../components/shared/Btn'
import styles from './pages.module.css'

type FormModalState = null | { mode: 'add' } | { mode: 'edit'; menu: Menu }
type OrderModalState = null | { menu: Menu }

export default function MenusPage() {
  const menus = useMenus()
  const [selected, setSelected] = useState<Menu | null>(null)
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
    await updateMenu(menu.name, data)
    setFormModal(null)
  }

  async function handleDelete(menu: Menu) {
    await deleteMenu(menu.name)
    if (selected?.name === menu.name) {
      setSelected(null)
    }
  }

  function openEdit(menu: Menu) {
    setFormModal({ mode: 'edit', menu })
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
            <MenuList menus={menus} query={query} onSelect={setSelected} onOrder={openOrder} />
          )}
        </div>
      </div>

      {selected && <MenuDetail menu={selected} onClose={() => setSelected(null)} onEdit={openEdit} onDelete={handleDelete} onOrder={openOrder} />}
      {formModal?.mode === 'add' && <MenuFormModal onSubmit={handleCreate} onClose={() => setFormModal(null)} />}
      {formModal?.mode === 'edit' && <MenuFormModal menu={formModal.menu} onSubmit={handleUpdate} onClose={() => setFormModal(null)} />}
      {orderModal && <OrderFormModal menuRef={{ name: orderModal.menu.name }} menu={orderModal.menu} onSubmit={handleOrder} onClose={() => setOrderModal(null)} />}
    </div>
  )
}
