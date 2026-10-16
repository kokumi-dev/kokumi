import { useEffect, useState } from 'react'

export interface GraphSelection {
  /** The key of the selected node. */
  selected: string | null
  /** Selects the node, or clears the selection when it is selected already. */
  toggle: (key: string) => void
  clear: () => void
}

/**
 * useGraphSelection holds which node of a graph is selected. A click
 * anywhere outside the nodes and the details, or Escape, clears it.
 */
export function useGraphSelection(): GraphSelection {
  const [selected, setSelected] = useState<string | null>(null)

  useEffect(() => {
    if (!selected) return
    const onPointerDown = (e: PointerEvent) => {
      const target = e.target as Element | null
      if (target?.closest('[data-graph-details], [data-graph-node]')) return
      setSelected(null)
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setSelected(null)
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [selected])

  return {
    selected,
    toggle: (key) => setSelected((current) => (current === key ? null : key)),
    clear: () => setSelected(null),
  }
}
