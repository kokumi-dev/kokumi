import { useEffect, useState } from 'react'
import type { ChartInfo, ResolvedSource } from '../api/types'
import { getChartInfo } from '../api/client'

/**
 * Loads the chart metadata of a resolved source, e.g. to show a chart's
 * default values next to a values editor. info is null while loading, when
 * the source is unset, or when it could not be read.
 */
export function useChartInfo(source: ResolvedSource | undefined, namespace: string): { info: ChartInfo | null; loading: boolean } {
  const oci = source?.oci ?? ''
  const version = source?.version ?? ''
  const pantryName = source?.pantryRef?.name
  const key = oci && version ? `${namespace}|${pantryName ?? ''}|${oci}@${version}` : ''
  const [result, setResult] = useState<{ key: string; info: ChartInfo | null } | null>(null)

  useEffect(() => {
    if (!oci || !version) return
    const loaded = `${namespace}|${pantryName ?? ''}|${oci}@${version}`
    let cancelled = false
    getChartInfo(oci, version, pantryName, namespace)
      .then((info) => { if (!cancelled) setResult({ key: loaded, info }) })
      .catch(() => { if (!cancelled) setResult({ key: loaded, info: null }) })
    return () => { cancelled = true }
  }, [oci, version, pantryName, namespace])

  const current = result?.key === key ? result : null
  return { info: current?.info ?? null, loading: !!key && !current }
}
