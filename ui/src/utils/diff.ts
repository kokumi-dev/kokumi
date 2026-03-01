import { diffLines } from 'diff'

export type DiffLineType = 'added' | 'removed' | 'context' | 'omitted'

export interface DiffLine {
  type: DiffLineType
  content: string
  lineNoBefore?: number
  lineNoAfter?: number
}

/**
 * Computes a line-by-line diff between two YAML strings.
 * Returns a flat list of DiffLine entries representing the full file diff.
 */
export function computeDiff(before: string, after: string): DiffLine[] {
  const changes = diffLines(before, after)
  const lines: DiffLine[] = []
  let lineBefore = 1
  let lineAfter = 1

  for (const change of changes) {
    const rawLines = change.value.split('\n')
    // diffLines includes a trailing empty string when the last line ends with \n
    const contentLines =
      rawLines[rawLines.length - 1] === ''
        ? rawLines.slice(0, -1)
        : rawLines

    for (const content of contentLines) {
      if (change.added) {
        lines.push({ type: 'added', content, lineNoAfter: lineAfter++ })
      } else if (change.removed) {
        lines.push({ type: 'removed', content, lineNoBefore: lineBefore++ })
      } else {
        lines.push({
          type: 'context',
          content,
          lineNoBefore: lineBefore++,
          lineNoAfter: lineAfter++,
        })
      }
    }
  }

  return lines
}

/**
 * Filters a DiffLine list to only show changed lines plus `contextSize`
 * lines of surrounding context. Collapsed runs are replaced by an `omitted`
 * sentinel describing how many lines were hidden.
 *
 * Passing contextSize = Infinity returns the full file (no collapsing).
 */
export function filterContext(
  lines: DiffLine[],
  contextSize: number,
): DiffLine[] {
  if (contextSize === Infinity) return lines

  const changed = new Set<number>()
  lines.forEach((l, i) => {
    if (l.type === 'added' || l.type === 'removed') {
      changed.add(i)
    }
  })

  if (changed.size === 0) {
    // No changes — omit whole file.
    return lines.length > 0
      ? [{ type: 'omitted', content: `${lines.length} lines unchanged` }]
      : []
  }

  const visible = new Set<number>()
  for (const idx of changed) {
    for (
      let i = Math.max(0, idx - contextSize);
      i <= Math.min(lines.length - 1, idx + contextSize);
      i++
    ) {
      visible.add(i)
    }
  }

  const result: DiffLine[] = []
  let prev = -1
  const sorted = [...visible].sort((a, b) => a - b)

  for (const idx of sorted) {
    if (prev !== -1 && idx > prev + 1) {
      const count = idx - prev - 1
      result.push({ type: 'omitted', content: `${count} line${count === 1 ? '' : 's'} unchanged` })
    }
    result.push(lines[idx])
    prev = idx
  }

  // Trailing omitted block
  const lastVisible = sorted[sorted.length - 1]
  const remaining = lines.length - 1 - lastVisible
  if (remaining > 0) {
    result.push({ type: 'omitted', content: `${remaining} line${remaining === 1 ? '' : 's'} unchanged` })
  }

  return result
}
