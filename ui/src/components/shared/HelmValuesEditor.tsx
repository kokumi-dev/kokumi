import { useRef, useState } from 'react'
import { load } from 'js-yaml'
import type { ChartInfo } from '../../api/types'
import { objectToYaml, yamlToValues } from '../../utils/yaml'
import { Disclosure } from '../ui'
import styles from './Form.module.css'

interface Props {
  values: Record<string, unknown>
  onChange: (values: Record<string, unknown>) => void
  /** The chart's defaults, offered as a searchable reference below the editor. */
  chartInfo: ChartInfo | null
  chartInfoLoading: boolean
}

/** YAML editor for Helm values with a reference of the chart's defaults. */
export default function HelmValuesEditor({ values, onChange, chartInfo, chartInfoLoading }: Props) {
  const [valuesYaml, setValuesYaml] = useState(() => objectToYaml(values))
  const [valuesError, setValuesError] = useState<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  function handleValuesChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    const text = e.target.value
    setValuesYaml(text)
    try {
      const parsed = yamlToValues(text)
      setValuesError(null)
      onChange(parsed)
    } catch (err) {
      setValuesError(err instanceof Error ? err.message : String(err))
    }
  }

  function jumpToPath(dotPath: string) {
    const el = textareaRef.current
    if (!el) return
    const segments = dotPath.split('.')
    const lines = valuesYaml.split('\n')
    // Walk lines tracking indent depth; match each path segment in order.
    let segIdx = 0
    let charOffset = 0
    let matchOffset = -1
    let matchLength = 0
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i]
      const trimmed = line.trimStart()
      const expectedIndent = segIdx * 2
      const actualIndent = line.length - trimmed.length
      const seg = segments[segIdx]
      if (
        actualIndent === expectedIndent &&
        trimmed.startsWith(`${seg}:`) &&
        (trimmed.length === seg.length + 1 || trimmed[seg.length + 1] === ' ')
      ) {
        if (segIdx === segments.length - 1) {
          matchOffset = charOffset
          matchLength = line.length
          break
        }
        segIdx++
      } else if (actualIndent < segIdx * 2) {
        // Went back up — restart from first segment
        segIdx = 0
        if (
          actualIndent === 0 &&
          trimmed.startsWith(`${segments[0]}:`) &&
          (trimmed.length === segments[0].length + 1 || trimmed[segments[0].length + 1] === ' ')
        ) {
          segIdx = 1
          if (segments.length === 1) {
            matchOffset = charOffset
            matchLength = line.length
            break
          }
        }
      }
      charOffset += line.length + 1
    }
    if (matchOffset === -1) return
    el.focus()
    el.setSelectionRange(matchOffset, matchOffset + matchLength)
    const lineIndex = valuesYaml.slice(0, matchOffset).split('\n').length - 1
    const lineHeight = parseFloat(getComputedStyle(el).lineHeight) || 20
    el.scrollTop = lineIndex * lineHeight - el.clientHeight / 3
  }

  const showReference =
    chartInfoLoading || (chartInfo !== null && chartInfo.isHelm)

  return (
    <>
      <div className={styles.fieldGroup}>
        <label className={styles.label}>Values (YAML)</label>
        <textarea
          ref={textareaRef}
          className={styles.valuesArea}
          value={valuesYaml}
          onChange={handleValuesChange}
          placeholder={'replicaCount: 2\nimage:\n  tag: v1.0.0'}
          spellCheck={false}
        />
        {valuesError && <p className={styles.valuesError}>{valuesError}</p>}
      </div>

      {showReference && (
        <ChartReferencePanel
          chartInfo={chartInfo}
          loading={chartInfoLoading}
          onJumpToPath={jumpToPath}
        />
      )}
    </>
  )
}

// ── ChartReferencePanel ───────────────────────────────────────────────────────

interface DefaultEntry {
  path: string
  value: string
}

/** Recursively flattens a nested object into dotted-path entries. */
function flattenValues(obj: unknown, prefix = ''): DefaultEntry[] {
  if (typeof obj !== 'object' || obj === null || Array.isArray(obj)) {
    return prefix ? [{ path: prefix, value: String(obj ?? '') }] : []
  }
  const entries: DefaultEntry[] = []
  for (const [key, val] of Object.entries(obj as Record<string, unknown>)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (typeof val === 'object' && val !== null && !Array.isArray(val)) {
      entries.push(...flattenValues(val, path))
    } else {
      entries.push({ path, value: String(val ?? '') })
    }
  }
  return entries
}

interface ChartReferencePanelProps {
  chartInfo: ChartInfo | null
  loading: boolean
  onJumpToPath: (dotPath: string) => void
}

function ChartReferencePanel({ chartInfo, loading, onJumpToPath }: ChartReferencePanelProps) {
  const [open, setOpen] = useState(false)
  const [readmeOpen, setReadmeOpen] = useState(false)
  const [query, setQuery] = useState('')

  const defaultEntries: DefaultEntry[] = (() => {
    if (!chartInfo?.isHelm || !chartInfo.defaultValues) return []
    try {
      const parsed = load(chartInfo.defaultValues)
      return flattenValues(parsed)
    } catch {
      return []
    }
  })()

  const filteredEntries = query
    ? defaultEntries.filter((e) => e.path.toLowerCase().includes(query.toLowerCase()))
    : defaultEntries

  const headerLabel = chartInfo?.isHelm
    ? `Chart Reference — ${chartInfo.name} ${chartInfo.chartVersion}`
    : 'Chart Reference'

  return (
    <Disclosure
      tone="inline"
      className={styles.chartRef}
      open={open}
      onOpenChange={setOpen}
      label={
        loading ? (
          <>
            <span className={styles.chartRefSpinner} />
            Loading chart info…
          </>
        ) : (
          headerLabel
        )
      }
    >
      {!loading && chartInfo?.isHelm && (
        <div className={styles.chartRefBody}>
          {chartInfo.description && (
            <p className={styles.chartRefDescription}>{chartInfo.description}</p>
          )}

          <input
            type="search"
            className={`${styles.input} ${styles.chartRefSearch}`}
            placeholder="Search defaults… e.g. replicas or server.replicas"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            spellCheck={false}
          />

          {filteredEntries.length === 0 && (
            <p className={styles.chartRefEmpty}>
              {query ? 'No matching defaults.' : 'No default values.'}
            </p>
          )}

          {filteredEntries.length > 0 && (
            <ul className={styles.chartRefList} aria-label="Default values">
              {filteredEntries.map((entry) => (
                <li
                  key={entry.path}
                  className={styles.chartRefItem}
                  onClick={() => onJumpToPath(entry.path)}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') onJumpToPath(entry.path)
                  }}
                  title={`Jump to ${entry.path} in Values editor`}
                >
                  <span className={styles.chartRefPath}>{entry.path}</span>
                  <span className={styles.chartRefValue}>{entry.value}</span>
                </li>
              ))}
            </ul>
          )}

          {chartInfo.readme && (
            <Disclosure
              tone="inline"
              className={styles.chartRefReadme}
              label="README"
              open={readmeOpen}
              onOpenChange={setReadmeOpen}
            >
              <pre className={styles.chartRefReadmeContent}>{chartInfo.readme}</pre>
            </Disclosure>
          )}
        </div>
      )}
    </Disclosure>
  )
}
