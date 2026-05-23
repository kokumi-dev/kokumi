import { useState } from 'react'
import type { Pantry, PantryFormData } from '../api/types'
import { createPantry, updatePantry, deletePantry } from '../api/client'
import { usePantries } from '../hooks/usePantries'
import PantryList from '../components/pantry/PantryList'
import PantryDetail from '../components/pantry/PantryDetail'
import PantryFormModal from '../components/pantry/PantryFormModal'
import Btn from '../components/shared/Btn'
import styles from './pages.module.css'

type FormModalState = null | { mode: 'add' } | { mode: 'edit'; pantry: Pantry }

export default function PantriesPage() {
  const pantries = usePantries()
  const [selected, setSelected] = useState<Pantry | null>(null)
  const [formModal, setFormModal] = useState<FormModalState>(null)
  const [query, setQuery] = useState('')

  async function handleCreate(data: PantryFormData) {
    await createPantry(data)
    setFormModal(null)
  }

  async function handleUpdate(data: PantryFormData) {
    if (formModal?.mode !== 'edit') return
    const { pantry } = formModal
    await updatePantry(pantry.namespace, pantry.name, data)
    setFormModal(null)
  }

  async function handleDelete(pantry: Pantry) {
    await deletePantry(pantry.namespace, pantry.name)
    if (selected?.name === pantry.name) setSelected(null)
  }

  function openEdit(pantry: Pantry) {
    setFormModal({ mode: 'edit', pantry })
  }

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Pantries</h1>
        <p className={styles.subtitle}>Manage OCI registry connections used by Orders</p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>All Pantries</span>
          <input
            className={styles.sectionSearch}
            type="search"
            placeholder="Filter by name…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <Btn variant="primary" size="sm" onClick={() => setFormModal({ mode: 'add' })}>
            + Add Pantry
          </Btn>
        </div>
        <div className={styles.sectionBody}>
          {pantries === null ? (
            <div className={styles.placeholder}>
              <span className={styles.placeholderText}>Loading…</span>
            </div>
          ) : (
            <PantryList
              pantries={pantries}
              selectedName={selected?.name}
              query={query}
              onSelect={setSelected}
            />
          )}
        </div>
      </div>

      {selected && (
        <PantryDetail
          pantry={selected}
          onClose={() => setSelected(null)}
          onEdit={openEdit}
          onDelete={handleDelete}
        />
      )}
      {formModal?.mode === 'add' && (
        <PantryFormModal onSubmit={handleCreate} onClose={() => setFormModal(null)} />
      )}
      {formModal?.mode === 'edit' && (
        <PantryFormModal
          pantry={formModal.pantry}
          onSubmit={handleUpdate}
          onClose={() => setFormModal(null)}
        />
      )}
    </div>
  )
}
