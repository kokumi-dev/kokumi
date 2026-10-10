import { useSearchParams } from 'react-router'

/** Keeps a list filter in the `?q=` query param so filtered views can be shared. */
export function useFilterParam(): [string, (value: string) => void] {
  const [params, setParams] = useSearchParams()
  const value = params.get('q') ?? ''
  const setValue = (next: string) => {
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev)
        if (next) p.set('q', next)
        else p.delete('q')
        return p
      },
      { replace: true },
    )
  }
  return [value, setValue]
}

export function matchesFilter(query: string, ...fields: (string | undefined)[]): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return fields.some((f) => f?.toLowerCase().includes(q))
}
