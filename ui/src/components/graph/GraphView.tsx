import { useState, type ReactNode } from 'react'
import { Button } from '../ui'
import styles from './GraphView.module.css'

/** A line of explanation shown with a graph. */
export interface Note {
  tone?: 'ok' | 'warning'
  text: ReactNode
}

interface Props {
  loading?: boolean
  /** Why the graph could not be loaded. */
  error?: string | null
  /** The items of the reading help, as <li>s, behind a toggle above the graph. */
  help?: ReactNode
  /** The details of the selected node, shown beside the graph. */
  inspector?: ReactNode
  /** Lines of explanation shown below the graph. */
  notes?: Note[]
  /** The graph. */
  children: ReactNode
}

/**
 * GraphView lays out a graph: centred on a stage that scrolls sideways when
 * it is wide, with the details of the selected node beside it.
 */
export default function GraphView({ loading, error, help, inspector, notes = [], children }: Props) {
  const [helpOpen, setHelpOpen] = useState(false)

  if (error) return <p className={styles.error}>{error}</p>
  if (loading) return <p className={styles.loading}>Loading…</p>

  return (
    <div className={styles.view}>
      {help && (
        <div className={styles.toolbar}>
          <Button variant="ghost" size="sm" onClick={() => setHelpOpen(!helpOpen)} aria-expanded={helpOpen}>
            {helpOpen ? 'Hide help' : 'How to read this'}
          </Button>
        </div>
      )}

      {help && helpOpen && <ul className={styles.help}>{help}</ul>}

      <div className={`${styles.body} ${inspector ? styles.bodyInspecting : ''}`}>
        <div className={styles.stage}>
          <div className={styles.stageInner}>{children}</div>
        </div>
        {inspector}
      </div>

      {notes.length > 0 && (
        <ul className={styles.notes}>
          {notes.map((note, i) => (
            <li key={i} className={note.tone === 'ok' ? styles.noteOk : note.tone === 'warning' ? styles.noteWarning : undefined}>
              {note.text}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
