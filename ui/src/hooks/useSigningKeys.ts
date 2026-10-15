import { useEffect, useState } from 'react'
import { getSigning } from '../api/client'

/**
 * Returns the names of the keys Kokumi trusts by key ID: "current" for the
 * signing key, else the trusted key's name. Empty until loaded.
 */
export function useSigningKeys(): ReadonlyMap<string, string> {
  const [keys, setKeys] = useState<ReadonlyMap<string, string>>(new Map())

  useEffect(() => {
    let cancelled = false
    getSigning()
      .then((s) => {
        if (cancelled) return
        const byID = new Map<string, string>()
        for (const k of [...(s.current ? [s.current] : []), ...s.trustedKeys]) {
          if (k.keyID) byID.set(k.keyID, k.name)
        }
        setKeys(byID)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  return keys
}

/** Describes the key with keyID by its trusted name when it has one. */
export function keyLabel(keys: ReadonlyMap<string, string>, keyID: string): string {
  const name = keys.get(keyID)
  if (name === 'current') return `the Kokumi signing key (${keyID})`
  return name ? `trusted key "${name}" (${keyID})` : `key ${keyID}`
}
