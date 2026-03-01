import { useState } from 'react'
import type { Recipe, RecipeFormData } from '../api/types'
import { createRecipe, updateRecipe, deleteRecipe } from '../api/client'
import { useRecipes } from '../hooks/useRecipes'
import RecipeList from '../components/recipe/RecipeList'
import RecipeDetail from '../components/recipe/RecipeDetail'
import RecipeFormModal from '../components/recipe/RecipeFormModal'
import Btn from '../components/shared/Btn'
import styles from './pages.module.css'

type FormModalState = null | { mode: 'add' } | { mode: 'edit'; recipe: Recipe }

export default function Recipes() {
  const recipes = useRecipes()
  const [selected, setSelected] = useState<Recipe | null>(null)
  const [formModal, setFormModal] = useState<FormModalState>(null)
  const [query, setQuery] = useState('')

  async function handleCreate(data: RecipeFormData) {
    await createRecipe(data)
    setFormModal(null)
  }

  async function handleUpdate(data: RecipeFormData) {
    if (formModal?.mode !== 'edit') return
    const { recipe } = formModal
    await updateRecipe(recipe.namespace, recipe.name, data)
    setFormModal(null)
  }

  async function handleDelete(recipe: Recipe) {
    await deleteRecipe(recipe.namespace, recipe.name)
    if (selected?.name === recipe.name && selected?.namespace === recipe.namespace) {
      setSelected(null)
    }
  }

  function openEdit(recipe: Recipe) {
    setFormModal({ mode: 'edit', recipe })
  }

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>Recipes</h1>
        <p className={styles.subtitle}>
          Manage your Recipe custom resources
        </p>
      </div>

      <div className={styles.section}>
        <div className={styles.sectionHeader}>
          <span className={styles.sectionTitle}>All Recipes</span>
          <input
            className={styles.sectionSearch}
            type="search"
            placeholder="Filter by name or namespace…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Filter recipes"
          />
          <Btn variant="primary" size="sm" onClick={() => setFormModal({ mode: 'add' })}>
            + Add Recipe
          </Btn>
        </div>
        <div className={styles.sectionBody}>
          {recipes === null ? (
            <div className={styles.placeholder}>
              <span className={styles.placeholderText}>Loading…</span>
            </div>
          ) : (
            <RecipeList
              recipes={recipes}
              query={query}
              onSelect={setSelected}
            />
          )}
        </div>
      </div>

      {selected && (
        <RecipeDetail
          recipe={selected}
          onClose={() => setSelected(null)}
          onEdit={openEdit}
          onDelete={handleDelete}
        />
      )}

      {formModal?.mode === 'add' && (
        <RecipeFormModal
          onSubmit={handleCreate}
          onClose={() => setFormModal(null)}
        />
      )}

      {formModal?.mode === 'edit' && (
        <RecipeFormModal
          recipe={formModal.recipe}
          onSubmit={handleUpdate}
          onClose={() => setFormModal(null)}
        />
      )}
    </div>
  )
}
