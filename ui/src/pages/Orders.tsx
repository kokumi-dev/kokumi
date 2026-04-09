import { useState } from 'react'
import type { Order, OrderFormData, Menu } from '../api/types'
import { createOrder, updateOrder, deleteOrder } from '../api/client'
import { useOrders } from '../hooks/useOrders'
import { useMenus } from '../hooks/useMenus'
import OrderList from '../components/recipe/OrderList'
import OrderDetail from '../components/recipe/OrderDetail'
import OrderFormModal from '../components/recipe/OrderFormModal'
import Btn from '../components/shared/Btn'
import styles from './pages.module.css'

type FormModalState = null | { mode: 'add' } | { mode: 'edit'; order: Order }

export default function OrdersPage() {
  const orders = useOrders()
  const menus = useMenus()
  const [selectedKey, setSelectedKey] = useState<{ namespace: string; name: string } | null>(null)
  const [formModal, setFormModal] = useState<FormModalState>(null)
  const [query, setQuery] = useState('')

  // Derive selected order from the live SSE-backed list so it stays fresh.
  const selected = selectedKey
    ? orders?.find((o) => o.namespace === selectedKey.namespace && o.name === selectedKey.name) ?? null
    : null

  // Resolve the menu linked to the selected order (if any).
  const selectedMenu: Menu | undefined = selected?.menuRef
    ? menus?.find((m) => m.name === selected.menuRef?.name)
    : undefined

  // Edits are allowed unless the menu explicitly forbids them.
  // When a menuRef is set but the menu hasn't loaded yet, default to denying
  // edits (fail closed) rather than allowing a forbidden action.
  const editsAllowed = selected?.menuRef
    ? !!selectedMenu && selectedMenu.overrides.patches.policy !== 'None'
    : true

  async function handleCreate(data: OrderFormData, commitMessage: string) {
    await createOrder(data, commitMessage)
    setFormModal(null)
  }

  async function handleUpdate(data: OrderFormData, commitMessage: string) {
    if (formModal?.mode !== 'edit') return
    const { order } = formModal
    await updateOrder(order.namespace, order.name, data, commitMessage)
    setFormModal(null)
  }

  async function handleDelete(order: Order) {
    await deleteOrder(order.namespace, order.name)
    if (selected?.name === order.name && selected?.namespace === order.namespace) {
      setSelectedKey(null)
    }
  }

  function openEdit(order: Order) {
    setFormModal({ mode: 'edit', order })
  }

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Orders</h1>
        <p className={styles.subtitle}>
          Manage your Order custom resources
        </p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>All Orders</span>
          <input
            className={styles.sectionSearch}
            type="search"
            placeholder="Filter by name or namespace…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Filter orders"
          />
          <Btn variant="primary" size="sm" onClick={() => setFormModal({ mode: 'add' })}>
            + Add Order
          </Btn>
        </div>
        <div className={styles.sectionBody}>
          {orders === null ? (
            <div className={styles.placeholder}>
              <span className={styles.placeholderText}>Loading…</span>
            </div>
          ) : (
            <OrderList
              orders={orders}
              query={query}
              onSelect={(o) => setSelectedKey({ namespace: o.namespace, name: o.name })}
            />
          )}
        </div>
      </div>

      {selected && (
        <OrderDetail
          order={selected}
          editsAllowed={editsAllowed}
          onClose={() => setSelectedKey(null)}
          onEdit={openEdit}
          onDelete={handleDelete}
        />
      )}

      {formModal?.mode === 'add' && (
        <OrderFormModal
          menus={menus ?? undefined}
          onSubmit={handleCreate}
          onClose={() => setFormModal(null)}
        />
      )}

      {formModal?.mode === 'edit' && (
        <OrderFormModal
          order={formModal.order}
          menu={formModal.order.menuRef
            ? menus?.find((m) => m.name === formModal.order.menuRef?.name)
            : undefined}
          onSubmit={handleUpdate}
          onClose={() => setFormModal(null)}
        />
      )}
    </div>
  )
}
