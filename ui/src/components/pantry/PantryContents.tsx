import { useEffect, useMemo, useState } from 'react'
import type { ArtifactInfo, Pantry } from '../../api/types'
import { getArtifactInfo, listOCITags } from '../../api/client'
import { cleanTags } from '../../api/ociTags'
import Badge from '../shared/Badge'
import Btn from '../shared/Btn'
import Modal from '../shared/Modal'
import YamlEditor from '../shared/YamlEditor'
import FileInspector from '../shared/FileInspector'
import styles from './PantryContents.module.css'

const PAGE_SIZE = 20

interface Props {
  pantry: Pantry
}

type Tab = 'readme' | 'values' | 'manifest'

interface ExpandedState {
  tag: string
  info: ArtifactInfo | null
  loading: boolean
  error: string | null
}

export default function PantryContents({ pantry }: Props) {
  const [allTags, setAllTags] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(0)
  const [expanded, setExpanded] = useState<ExpandedState | null>(null)
  const [manifestModal, setManifestModal] = useState<{ tag: string; content: string } | null>(null)

  const ref = pantry.url

  function loadTags() {
    // No synchronous setState here: loading is initialised to true so the first
    // render already shows the loading state, and the Refresh button sets it
    // again from an event handler. All setState calls below run in async
    // callbacks, which the effect lint rule permits.
    listOCITags(ref, pantry.name, pantry.namespace)
      .then((tags) => {
        setAllTags(cleanTags(tags))
        setPage(0)
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    loadTags()
    // Switching pantries remounts this component (via key in PantryDetail),
    // so all state is reset without a synchronous setState here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pantry.name, pantry.namespace])

  const filtered = useMemo(() => {
    if (!query) return allTags
    const q = query.toLowerCase()
    return allTags.filter((t) => t.toLowerCase().includes(q))
  }, [allTags, query])

  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const safePage = Math.min(page, pageCount - 1)
  const pageTags = filtered.slice(safePage * PAGE_SIZE, safePage * PAGE_SIZE + PAGE_SIZE)

  function toggle(tag: string) {
    if (expanded?.tag === tag) {
      setExpanded(null)
      return
    }
    setExpanded({ tag, info: null, loading: true, error: null })
    getArtifactInfo(ref, tag, pantry.name, pantry.namespace)
      .then((info) => setExpanded({ tag, info, loading: false, error: null }))
      .catch((e: Error) => setExpanded({ tag, info: null, loading: false, error: e.message }))
  }

  return (
    <div className={styles.wrap}>
      <div className={styles.toolbar}>
        <input
          className={styles.search}
          type="search"
          placeholder="Filter tags…"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setPage(0)
          }}
        />
        <div className={styles.pager}>
          <Btn
            variant="ghost"
            size="sm"
            disabled={loading}
            onClick={() => {
              setLoading(true)
              setError(null)
              loadTags()
            }}
          >
            Refresh
          </Btn>
          <Btn
            variant="ghost"
            size="sm"
            disabled={loading || safePage <= 0}
            onClick={() => setPage((p) => Math.max(0, p - 1))}
          >
            ‹ Prev
          </Btn>
          <span className={styles.pageIndicator}>
            {safePage + 1} / {pageCount}
          </span>
          <Btn
            variant="ghost"
            size="sm"
            disabled={loading || safePage >= pageCount - 1}
            onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
          >
            Next ›
          </Btn>
        </div>
      </div>

      {loading && allTags.length === 0 ? (
        <div className={styles.placeholder}>Loading tags…</div>
      ) : error ? (
        <div className={styles.placeholder}>{error}</div>
      ) : filtered.length === 0 ? (
        <div className={styles.placeholder}>No tags found</div>
      ) : (
        <ul className={styles.list}>
          {pageTags.map((tag) => {
            const isOpen = expanded?.tag === tag
            return (
              <li key={tag} className={styles.row}>
                <button className={styles.rowHeader} onClick={() => toggle(tag)} aria-expanded={isOpen}>
                  <span className={`${styles.chevron} ${isOpen ? styles.chevronOpen : ''}`}>›</span>
                  <span className={styles.tagName}>{tag}</span>
                  {isOpen && expanded?.info && (
                    <Badge state={expanded.info.isHelm ? 'Helm' : 'Manifest'} />
                  )}
                </button>

                {isOpen && (
                  <div className={styles.detail}>
                    {expanded.loading && <div className={styles.placeholder}>Loading…</div>}
                    {expanded.error && <div className={styles.placeholder}>{expanded.error}</div>}
                    {expanded.info && <ArtifactDetail info={expanded.info} onViewManifest={(c) => setManifestModal({ tag, content: c })} />}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}

      {manifestModal && (
        <Modal title={`Manifest — ${manifestModal.tag}`} onClose={() => setManifestModal(null)} wide>
          <YamlEditor value={manifestModal.content} readOnly tall />
        </Modal>
      )}
    </div>
  )
}

function ArtifactDetail({ info, onViewManifest }: { info: ArtifactInfo; onViewManifest: (content: string) => void }) {
  const [tab, setTab] = useState<Tab>(info.isHelm ? 'readme' : 'manifest')

  return (
    <div className={styles.artifact}>
      {info.digest && (
        <div className={styles.metaRow}>
          <span className={styles.metaKey}>Digest</span>
          <span className={styles.metaValue}>{info.digest}</span>
        </div>
      )}

      {info.isHelm && info.chartInfo && (
        <div className={styles.metaRow}>
          <span className={styles.metaKey}>Chart</span>
          <span className={styles.metaValue}>
            {info.chartInfo.name}
            {info.chartInfo.version ? ` ${info.chartInfo.version}` : ''}
            {info.chartInfo.appVersion ? ` (app ${info.chartInfo.appVersion})` : ''}
          </span>
        </div>
      )}

      <div className={styles.tabs}>
        {info.isHelm && (
          <>
            <button className={`${styles.tab} ${tab === 'readme' ? styles.tabActive : ''}`} onClick={() => setTab('readme')}>
              README
            </button>
            <button className={`${styles.tab} ${tab === 'values' ? styles.tabActive : ''}`} onClick={() => setTab('values')}>
              Values
            </button>
          </>
        )}
        {!info.isHelm && (
          <button className={`${styles.tab} ${tab === 'manifest' ? styles.tabActive : ''}`} onClick={() => setTab('manifest')}>
            Manifest
          </button>
        )}
      </div>

      <div className={styles.tabBody}>
        {tab === 'readme' && <pre className={styles.pre}>{info.chartInfo?.readme || 'No README.'}</pre>}
        {tab === 'values' && <pre className={styles.pre}>{info.chartInfo?.defaultValues || 'No default values.'}</pre>}
        {tab === 'manifest' && (
          <div className={styles.manifestInline}>
            {info.files && info.files.length > 1 ? (
              <FileInspector files={info.files} />
            ) : (
              <>
                <pre className={styles.pre}>{info.manifest}</pre>
                <Btn variant="secondary" size="sm" onClick={() => onViewManifest(info.manifest ?? '')}>
                  Open in viewer
                </Btn>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
