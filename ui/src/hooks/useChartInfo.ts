import { useEffect, useState } from 'react'
import type { ChartInfo, RegistryRef, ResolvedSource } from '../api/types'
import { getChartInfo } from '../api/client'

/**
 * Loads the chart metadata of a Product's resolved source through the
 * Product's Registries, e.g. to show a chart's default values next to a
 * values editor. info is null while loading, when the source is unset, or
 * when it could not be read.
 */
export function useChartInfo(
  source: ResolvedSource | undefined,
  namespace: string,
  registryRefs: RegistryRef[] | undefined,
): { info: ChartInfo | null; loading: boolean } {
  const url = source?.url ?? ''
  const version = source?.version ?? ''
  const refs = (registryRefs ?? []).map((r) => r.name).join(',')
  const key = url && version ? `${namespace}|${refs}|${url}@${version}` : ''
  const [result, setResult] = useState<{ key: string; info: ChartInfo | null } | null>(null)

  useEffect(() => {
    if (!url || !version) return
    const loaded = `${namespace}|${refs}|${url}@${version}`
    let cancelled = false
    getChartInfo(url, version, { namespace, registryRefs: refs ? refs.split(',') : [] })
      .then((info) => { if (!cancelled) setResult({ key: loaded, info }) })
      .catch(() => { if (!cancelled) setResult({ key: loaded, info: null }) })
    return () => { cancelled = true }
  }, [url, version, refs, namespace])

  const current = result?.key === key ? result : null
  return { info: current?.info ?? null, loading: !!key && !current }
}
