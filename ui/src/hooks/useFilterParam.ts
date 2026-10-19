import { useSearchParams } from 'react-router'

/**
 * Keeps a value of the view in a query param, so it survives reloads and
 * can be shared. Changes replace the history entry; empty removes it.
 */
export function useQueryParam(name: string): [string, (value: string) => void] {
  const [params, setParams] = useSearchParams()
  const value = params.get(name) ?? ''
  const setValue = (next: string) => {
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev)
        if (next) p.set(name, next)
        else p.delete(name)
        return p
      },
      { replace: true },
    )
  }
  return [value, setValue]
}

/** Keeps a list filter in the `?q=` query param so filtered views can be shared. */
export function useFilterParam(): [string, (value: string) => void] {
  return useQueryParam('q')
}

export function matchesFilter(query: string, ...fields: (string | undefined)[]): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return fields.some((f) => f?.toLowerCase().includes(q))
}
