import { useState } from 'react'
import type { Pantry } from '../../api/types'
import { listPantryRepositories, listPantryTags } from '../../api/client'
import Badge from '../shared/Badge'
import Btn from '../shared/Btn'
import styles from './PantryDetail.module.css'

interface Props {
  pantry: Pantry
  onClose: () => void
  onEdit: (pantry: Pantry) => void
  onDelete: (pantry: Pantry) => void
}

export default function PantryDetail({ pantry, onClose, onEdit, onDelete }: Props) {
  return (
    <>
      <div className={styles.backdrop} onClick={onClose} />
      <div className={styles.panel}>
        {/* Header */}
        <div className={styles.header}>
          <div className={styles.headerLeft}>
            <span className={styles.title}>{pantry.name}</span>
            <span className={styles.subtitle}>{pantry.namespace}</span>
          </div>
          <div className={styles.headerActions}>
            <Badge state={pantry.state ?? ''} />
            <Btn variant="secondary" size="sm" onClick={() => onEdit(pantry)}>Edit</Btn>
            <Btn variant="danger" size="sm" onClick={() => onDelete(pantry)}>Delete</Btn>
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
              <span className={styles.specKey}>Registry</span>
              <span className={styles.specValue}>{pantry.registry}</span>
              {pantry.secretRef && (
                <>
                  <span className={styles.specKey}>Secret</span>
                  <span className={styles.specValue}>{pantry.secretRef}</span>
                </>
              )}
              {pantry.description && (
                <>
                  <span className={styles.specKey}>Description</span>
                  <span className={styles.specValue}>{pantry.description}</span>
                </>
              )}
            </div>
          </div>

          {/* Conditions */}
          {pantry.conditions && pantry.conditions.length > 0 && (
            <div className={styles.section}>
              <span className={styles.sectionTitle}>Conditions</span>
              <div className={styles.conditionList}>
                {pantry.conditions.map((c) => (
                  <div key={c.type} className={styles.condition}>
                    <div className={styles.conditionHeader}>
                      <span className={styles.conditionType}>{c.type}</span>
                      <span
                        className={`${styles.conditionStatus} ${
                          c.status === 'True' ? styles.conditionStatusTrue : styles.conditionStatusFalse
                        }`}
                      >
                        {c.status}
                      </span>
                      {c.reason && <span className={styles.conditionMessage}>{c.reason}</span>}
                    </div>
                    {c.message && <div className={styles.conditionMessage}>{c.message}</div>}
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Repository browser */}
          <RepositoryBrowser pantryNamespace={pantry.namespace} pantryName={pantry.name} />
        </div>
      </div>
    </>
  )
}

function RepositoryBrowser({ pantryNamespace, pantryName }: { pantryNamespace: string; pantryName: string }) {
  const [repos, setRepos] = useState<string[] | null>(null)
  const [reposError, setReposError] = useState<string | null>(null)
  const [openRepo, setOpenRepo] = useState<string | null>(null)
  const [tags, setTags] = useState<Record<string, string[] | 'loading' | string>>({})

  function loadRepos() {
    if (repos !== null) return
    listPantryRepositories(pantryNamespace, pantryName)
      .then((list) => setRepos(list.map((r) => r.name)))
      .catch((err: Error) => setReposError(err.message))
  }

  function toggleRepo(repoName: string) {
    if (openRepo === repoName) {
      setOpenRepo(null)
      return
    }
    setOpenRepo(repoName)
    if (tags[repoName] !== undefined) return
    setTags((prev) => ({ ...prev, [repoName]: 'loading' }))
    listPantryTags(pantryNamespace, pantryName, repoName)
      .then((t) => setTags((prev) => ({ ...prev, [repoName]: t })))
      .catch((err: Error) => setTags((prev) => ({ ...prev, [repoName]: err.message })))
  }

  return (
    <div className={styles.section}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <span className={styles.sectionTitle}>Repositories</span>
        {repos === null && (
          <Btn variant="secondary" size="sm" onClick={loadRepos}>Browse</Btn>
        )}
      </div>
      {reposError && <div className={styles.errorText}>{reposError}</div>}
      {repos !== null && (
        <div className={styles.repoList}>
          {repos.length === 0 && (
            <div className={styles.emptyText}>No repositories found</div>
          )}
          {repos.map((repo) => (
            <div key={repo}>
              <div
                className={`${styles.repoItem} ${openRepo === repo ? styles.repoItemActive : ''}`}
                onClick={() => toggleRepo(repo)}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => e.key === 'Enter' && toggleRepo(repo)}
              >
                <span className={styles.repoName}>{repo}</span>
                <svg
                  className={`${styles.chevron} ${openRepo === repo ? styles.chevronOpen : ''}`}
                  viewBox="0 0 10 10" width="10" height="10" fill="none"
                  stroke="currentColor" strokeWidth="1.8" strokeLinecap="round"
                >
                  <path d="M3 2l4 3-4 3" />
                </svg>
              </div>
              {openRepo === repo && (
                <div className={styles.tagList}>
                  {tags[repo] === 'loading' && <div className={styles.loadingText}>Loading tags…</div>}
                  {typeof tags[repo] === 'string' && tags[repo] !== 'loading' && (
                    <div className={styles.errorText}>{tags[repo] as string}</div>
                  )}
                  {Array.isArray(tags[repo]) && (tags[repo] as string[]).length === 0 && (
                    <div className={styles.emptyText}>No tags</div>
                  )}
                  {Array.isArray(tags[repo]) && (tags[repo] as string[]).map((tag) => (
                    <div key={tag} className={styles.tagItem}>{tag}</div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
