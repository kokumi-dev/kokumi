import { useMemo, useState } from 'react'
import type { ArtifactFile } from '../../api/types'
import YamlEditor from './YamlEditor'
import styles from './FileInspector.module.css'

interface Props {
  files: ArtifactFile[]
  /** When set, an Edit/Save toggle is shown for the selected file. */
  editable?: boolean
  /** Called with all files (edited content applied) when the user saves. */
  onSave?: (files: ArtifactFile[]) => void
  saving?: boolean
}

/**
 * FileInspector shows a GitHub-style file browser for multi-file manifest
 * artifacts: a file list on the left, the selected file's YAML on the right.
 * With `editable` and `onSave`, individual files can be edited in place.
 */
export default function FileInspector({ files, editable = false, onSave, saving = false }: Props) {
  const [selectedPath, setSelectedPath] = useState(files[0]?.path ?? '')
  const [editing, setEditing] = useState(false)
  const [drafts, setDrafts] = useState<Record<string, string>>({})

  const selected = useMemo(
    () => files.find((f) => f.path === selectedPath) ?? files[0],
    [files, selectedPath],
  )

  if (!selected) return null

  const isKustomization = /(^|\/)kustomization\.ya?ml$/.test(selected.path)
  const canEdit = editable && !!onSave && !isKustomization
  const content = drafts[selected.path] ?? selected.content
  const dirty = Object.keys(drafts).length > 0

  function selectFile(path: string) {
    setSelectedPath(path)
    setEditing(false)
  }

  function handleSave() {
    if (!onSave) return
    onSave(files.map((f) => (drafts[f.path] !== undefined ? { ...f, content: drafts[f.path] } : f)))
    setEditing(false)
    setDrafts({})
  }

  function handleDiscard() {
    setEditing(false)
    setDrafts({})
  }

  return (
    <div className={styles.inspector}>
      <div className={styles.fileList}>
        {files.map((f) => (
          <button
            key={f.path}
            type="button"
            className={`${styles.fileRow} ${f.path === selected.path ? styles.fileRowActive : ''}`}
            onClick={() => selectFile(f.path)}
          >
            <span className={styles.fileIcon} aria-hidden>
              {f.path === selected.path ? '▾' : '▸'}
            </span>
            {f.path}
            {drafts[f.path] !== undefined && <span className={styles.dirtyDot} title="modified" />}
          </button>
        ))}
      </div>
      <div className={styles.fileContent}>
        <div className={styles.fileHeader}>
          <span className={styles.fileName}>{selected.path}</span>
          {canEdit && !editing && (
            <button type="button" className={styles.headerBtn} onClick={() => setEditing(true)}>
              Edit
            </button>
          )}
          {editing && (
            <>
              <button
                type="button"
                className={styles.headerBtn}
                onClick={handleSave}
                disabled={saving || !dirty}
              >
                {saving ? 'Saving…' : 'Save'}
              </button>
              <button type="button" className={styles.headerBtn} onClick={handleDiscard} disabled={saving}>
                Discard
              </button>
            </>
          )}
        </div>
        <YamlEditor
          key={`${selected.path}-${editing}`}
          value={content}
          readOnly={!editing}
          onChange={editing ? (v) => setDrafts((d) => ({ ...d, [selected.path]: v })) : undefined}
          tall
        />
      </div>
    </div>
  )
}
