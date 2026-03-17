import { useState } from 'react'
import type { Order, OrderFormData } from '../api/types'
import { createOrder, updateOrder, deleteOrder } from '../api/client'
import { useOrders } from '../hooks/useOrders'
import OrderList from '../components/recipe/OrderList'
import OrderDetail from '../components/recipe/OrderDetail'
import OrderFormModal from '../components/recipe/OrderFormModal'
import Btn from '../components/shared/Btn'
import styles from './pages.module.css'

type FormModalState = null | { mode: 'add' } | { mode: 'edit'; order: Order }

export default function OrdersPage() {
  const orders = useOrders()
  const [selected, setSelected] = useState<Order | null>(null)
  const [formModal, setFormModal] = useState<FormModalState>(null)
  const [query, setQuery] = useState('')

  async function handleCreate(data: OrderFormData) {
    await createOrder(data)
    setFormModal(null)
  }

  async function handleUpdate(data: OrderFormData) {
    if (formModal?.mode !== 'edit') return
    const { order } = formModal
    await updateOrder(order.namespace, order.name, data)
    setFormModal(null)
  }

  async function handleDelete(order: Order) {
    await deleteOrder(order.namespace, order.name)
    if (selected?.name === order.name && selected?.namespace === order.namespace) {
      setSelected(null)
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
              onSelect={setSelected}
            />
          )}
        </div>
      </div>

      {selected && (
        <OrderDetail
          order={selected}
          onClose={() => setSelected(null)}
          onEdit={openEdit}
          onDelete={handleDelete}
        />
      )}

      {formModal?.mode === 'add' && (
        <OrderFormModal
          onSubmit={handleCreate}
          onClose={() => setFormModal(null)}
        />
      )}

      {formModal?.mode === 'edit' && (
        <OrderFormModal
          order={formModal.order}
          onSubmit={handleUpdate}
          onClose={() => setFormModal(null)}
        />
      )}
    </div>
  )
}
