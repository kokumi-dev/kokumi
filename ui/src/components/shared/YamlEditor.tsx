import { useEffect, useRef } from 'react'
import { EditorState } from '@codemirror/state'
import {
  EditorView,
  keymap,
  lineNumbers,
  highlightActiveLine,
  highlightActiveLineGutter,
} from '@codemirror/view'
import { defaultKeymap, historyKeymap, history } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { oneDark } from '@codemirror/theme-one-dark'
import styles from './YamlEditor.module.css'

interface Props {
  value: string
  onChange?: (value: string) => void
  /** When true the editor is read-only and has no cursor. Default: false. */
  readOnly?: boolean
  /** Make the editor taller (560 px scroller cap instead of 400 px). */
  tall?: boolean
}

/**
 * YamlEditor wraps a CodeMirror 6 editor themed to the Kokumi dark palette.
 * Pass `readOnly` for manifest/diff display, omit for form editing.
 */
export default function YamlEditor({ value, onChange, readOnly = false, tall = false }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)

  useEffect(() => {
    if (!containerRef.current) return

    const extensions = [
      yaml(),
      oneDark,
      lineNumbers(),
      highlightActiveLine(),
      highlightActiveLineGutter(),
      history(),
      keymap.of([...defaultKeymap, ...historyKeymap]),
      EditorView.lineWrapping,
    ]

    if (readOnly) {
      extensions.push(EditorState.readOnly.of(true))
    }

    if (onChange && !readOnly) {
      extensions.push(
        EditorView.updateListener.of((update) => {
          if (update.docChanged) {
            onChange(update.state.doc.toString())
          }
        }),
      )
    }

    const state = EditorState.create({ doc: value, extensions })
    const view = new EditorView({ state, parent: containerRef.current })
    viewRef.current = view

    return () => {
      view.destroy()
      viewRef.current = null
    }
    // Intentionally only initialises once; value sync is handled below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [readOnly])

  // Sync value changes without destroying/recreating the editor.
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const current = view.state.doc.toString()
    if (current === value) return
    view.dispatch({
      changes: { from: 0, to: current.length, insert: value },
    })
  }, [value])

  return (
    <div
      ref={containerRef}
      className={`${styles.wrap} ${tall ? styles.wrapTall : ''}`}
    />
  )
}
