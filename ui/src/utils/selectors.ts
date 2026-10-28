import type { LabelSelector } from '../api/types'

/** The requirements of a selector in kubectl's syntax, e.g. ["env=prod", "region in (eu,us)", "!deprecated"]. */
function requirements(selector: LabelSelector | undefined): string[] {
  return [
    ...Object.entries(selector?.matchLabels ?? {}).map(([k, v]) => `${k}=${v}`),
    ...(selector?.matchExpressions ?? []).map((e) => {
      switch (e.operator) {
        case 'In':
          return `${e.key} in (${(e.values ?? []).join(',')})`
        case 'NotIn':
          return `${e.key} notin (${(e.values ?? []).join(',')})`
        case 'Exists':
          return e.key
        case 'DoesNotExist':
          return `!${e.key}`
      }
    }),
  ]
}

/**
 * A selector in kubectl's syntax, as the server's labelSelector parameter
 * takes it, e.g. "env=prod,region in (eu,us),!deprecated"; empty for one
 * that selects everything.
 */
export function selectorString(selector: LabelSelector): string {
  return requirements(selector).join(',')
}

/**
 * How a selector reads, in kubectl's syntax, e.g. "env=prod, region in
 * (eu,us), !deprecated"; everything for one without requirements.
 */
export function describeSelector(selector: LabelSelector | undefined, everything: string): string {
  const parts = requirements(selector)
  return parts.length > 0 ? parts.join(', ') : everything
}
