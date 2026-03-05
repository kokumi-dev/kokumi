import { useState } from 'react'
import type { Recipe, Preparation } from '../../api/types'
import { promote } from '../../api/client'
import { usePreparations } from '../../hooks/usePreparations'
import Badge from '../shared/Badge'
import Btn from '../shared/Btn'
import PreparationList from './PreparationList'
import ManifestModal from './ManifestModal'
import DiffModal from './DiffModal'
import styles from './RecipeDetail.module.css'

interface Props {
  recipe: Recipe
  onClose: () => void
  onEdit: (recipe: Recipe) => void
  onDelete: (recipe: Recipe) => void
}

type ModalState =
  | null
  | { kind: 'manifest'; prep: Preparation }
  | { kind: 'diff'; prep: Preparation; activePrep: Preparation }

/**
 * RecipeDetail is a slide-in right panel that displays the full Recipe spec,
 * status conditions, and the live list of Preparations for that Recipe.
 */
export default function RecipeDetail({ recipe, onClose, onEdit, onDelete }: Props) {
  const preparations = usePreparations(recipe.name) ?? []
  const [modal, setModal] = useState<ModalState>(null)

  const activePrep = preparations.find((p) => p.isActive)

  async function handlePromote(prep: Preparation) {
    await promote(recipe.namespace, recipe.name, prep.name)
  }

  function handleOpenManifest(prep: Preparation) {
    setModal({ kind: 'manifest', prep })
  }

  function handleOpenDiff(prep: Preparation) {
    if (!activePrep) return
    setModal({ kind: 'diff', prep, activePrep })
  }

  return (
    <>
      {/* Backdrop */}
      <div className={styles.backdrop} onClick={onClose} />

      {/* Slide-in panel */}
      <div className={styles.panel}>
        {/* Header */}
        <div className={styles.header}>
          <div className={styles.headerLeft}>
            <span className={styles.title}>{recipe.name}</span>
            <span className={styles.subtitle}>{recipe.namespace}</span>
          </div>
          <div className={styles.headerActions}>
            <Badge phase={recipe.phase} />
            <Btn variant="secondary" size="sm" onClick={() => onEdit(recipe)}>
              Edit
            </Btn>
            <Btn variant="danger" size="sm" onClick={() => onDelete(recipe)}>
              Delete
            </Btn>
            <button className={styles.closeBtn} onClick={onClose} aria-label="Close panel">
              <svg viewBox="0 0 14 14" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                <path d="M2 2l10 10M12 2L2 12" />
              </svg>
            </button>
          </div>
        </div>

        {/* Body */}
        <div className={styles.body}>
          {/* Spec */}
          <div className={styles.section}>
            <span className={styles.sectionTitle}>Spec</span>
            <div className={styles.specGrid}>
              <span className={styles.specKey}>Source OCI</span>
              <span className={styles.specValue}>{recipe.source.oci}</span>
              <span className={styles.specKey}>Version</span>
              <span className={styles.specValue}>{recipe.source.version}</span>
              <span className={styles.specKey}>Destination</span>
              <span className={styles.specValue}>{recipe.destination.oci}</span>
              <span className={styles.specKey}>Auto Deploy</span>
              <span className={styles.specValue}>{recipe.autoDeploy ? 'Yes' : 'No'}</span>
              {recipe.render?.helm && (
                <>
                  <span className={styles.specKey}>Renderer</span>
                  <span className={styles.specValue}>Helm</span>
                  {recipe.render.helm.releaseName && (
                    <>
                      <span className={styles.specKey}>Release Name</span>
                      <span className={styles.specValue}>{recipe.render.helm.releaseName}</span>
                    </>
                  )}
                  {recipe.render.helm.namespace && (
                    <>
                      <span className={styles.specKey}>Helm Namespace</span>
                      <span className={styles.specValue}>{recipe.render.helm.namespace}</span>
                    </>
                  )}
                  <span className={styles.specKey}>Include CRDs</span>
                  <span className={styles.specValue}>{recipe.render.helm.includeCRDs ? 'Yes' : 'No'}</span>
                </>
              )}
              {recipe.activePreparation && (
                <>
                  <span className={styles.specKey}>Active Prep</span>
                  <span className={styles.specValue}>{recipe.activePreparation}</span>
                </>
              )}
              {recipe.latestRevision && (
                <>
                  <span className={styles.specKey}>Latest Rev</span>
                  <span className={styles.specValue}>{recipe.latestRevision}</span>
                </>
              )}
            </div>
          </div>

          {/* Patches */}
          {recipe.patches && recipe.patches.length > 0 && (
            <div className={styles.section}>
              <span className={styles.sectionTitle}>
                Patches ({recipe.patches.length})
              </span>
              <div className={styles.patchesList}>
                {recipe.patches.map((p, i) => (
                  <div key={i} className={styles.patchItem}>
                    <span className={styles.patchTarget}>
                      {p.target.kind}/{p.target.name}
                      {p.target.namespace ? ` (${p.target.namespace})` : ''}
                    </span>
                    {Object.entries(p.set).map(([k, v]) => (
                      <div key={k} className={styles.patchSetRow}>
                        <span className={styles.patchSetKey}>{k}</span>
                        <span>→</span>
                        <span>{v}</span>
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Conditions */}
          {recipe.conditions && recipe.conditions.length > 0 && (
            <div className={styles.section}>
              <span className={styles.sectionTitle}>Conditions</span>
              <div className={styles.conditionsList}>
                {recipe.conditions.map((c) => (
                  <div key={c.type} className={styles.conditionItem}>
                    <div className={styles.conditionHeader}>
                      <span className={styles.conditionType}>{c.type}</span>
                      <Badge phase={c.status === 'True' ? 'Ready' : c.status === 'False' ? 'Failed' : 'Pending'} />
                    </div>
                    {c.message && (
                      <span className={styles.conditionMessage}>{c.message}</span>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}

          <hr className={styles.divider} />

          {/* Preparations */}
          <div className={styles.section}>
            <span className={styles.sectionTitle}>
              Preparations
            </span>
            <PreparationList
              preparations={preparations}
              onPromote={handlePromote}
              onManifest={handleOpenManifest}
              onDiff={handleOpenDiff}
            />
          </div>
        </div>
      </div>

      {/* Sub-modals */}
      {modal?.kind === 'manifest' && (
        <ManifestModal
          preparation={modal.prep}
          onClose={() => setModal(null)}
        />
      )}

      {modal?.kind === 'diff' && (
        <DiffModal
          preparation={modal.prep}
          activePreparation={modal.activePrep}
          onClose={() => setModal(null)}
        />
      )}
    </>
  )
}
